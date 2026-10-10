import {
  createAgentAction,
  createAgentProfileAction,
  deleteAgentProfileAction,
  updateAgentAction,
  updateAgentProfileAction,
  updateAgentProfileMcpConfigAction,
} from "@/app/actions/agents";
import type {
  Agent,
  AgentProfile,
  McpServerDef,
  PermissionSetting,
  ModelConfig,
  ProfileEnvVar,
} from "@/lib/types/http";
import { arePermissionsDirty, permissionsToProfilePatch } from "@/lib/agent-permissions";
import { isProviderConfigDirty } from "@/components/settings/agent-profile-dirty";
import { areCLIFlagsEqual } from "@/lib/cli-flags";
import { areConfigOptionsEqual } from "@/lib/config-options";
import { t } from "@/lib/i18n";
import { toAgentProfilePayload } from "@/lib/api/domains/agent-profile-normalize";
import type { ProfileFormData } from "@/components/settings/profile-form-fields";
import type { AgentCreationPublication } from "@/hooks/domains/settings/use-agent-creation-store-sync";

// The JSON key the MCP editor validates against — an identifier, interpolated
// into the parse error rather than written into the catalog.
const MCP_SERVERS_KEY = "mcpServers";

const profilePatchFieldMap: Array<[keyof ProfileFormData, keyof AgentProfile]> = [
  ["name", "name"],
  ["model", "model"],
  ["fallback_model", "fallbackModel"],
  ["auto_fallback", "autoFallback"],
  ["require_exact_model", "requireExactModel"],
  ["mode", "mode"],
  ["config_options", "configOptions"],
  ["allow_indexing", "allowIndexing"],
  ["auto_approve", "autoApprove"],
  ["cli_passthrough", "cliPassthrough"],
  ["cursor_mcp_auth_enabled", "cursorMcpAuthEnabled"],
  ["cursor_plugins_mcp_enabled", "cursorPluginsMcpEnabled"],
  ["mcp_selection_mode", "mcpSelectionMode"],
  ["mcp_selected_servers", "mcpSelectedServers"],
  ["cli_flags", "cliFlags"],
  ["command_prefix", "commandPrefix"],
  ["provider_kind", "providerKind"],
];

/**
 * Translates a ProfileFormData patch (snake_case form keys) into a
 * Partial<AgentProfile> (camelCase). Profiles in client state use the
 * canonical camelCase AgentProfile shape, so without this translation
 * patches like { cli_passthrough: true } would land as a new snake_case
 * key and the camelCase reader would never see them.
 */
export function toAgentProfilePatch(patch: Partial<ProfileFormData>): Partial<AgentProfile> {
  const next: Partial<AgentProfile> = {};
  for (const [formKey, profileKey] of profilePatchFieldMap) {
    if (patch[formKey] !== undefined) {
      (next as Record<string, unknown>)[profileKey] = patch[formKey];
    }
  }
  return next;
}

/**
 * OpenAI-compatible provider fields for a profile save payload. The backend
 * normalizes them (clears everything when the kind is not
 * `openai_compatible`), so sending the cleared triple is safe and lets a
 * switch back to Native persist.
 */
function providerPayloadFields(profile: DraftProfile) {
  return {
    provider_kind: profile.providerKind ?? "",
    provider_base_url: profile.providerBaseUrl ?? "",
    provider_api_key_secret_id: profile.providerApiKeySecretId ?? "",
  };
}

function areEnvVarsEqual(a?: ProfileEnvVar[], b?: ProfileEnvVar[]): boolean {
  const left = a ?? [];
  const right = b ?? [];
  if (left.length !== right.length) return false;
  return left.every(
    (ev, i) =>
      ev.key === right[i]?.key &&
      (ev.value ?? "") === (right[i]?.value ?? "") &&
      (ev.secret_id ?? "") === (right[i]?.secret_id ?? ""),
  );
}

type DraftMcpConfig = {
  enabled: boolean;
  servers: string;
  dirty: boolean;
  error: string | null;
};

/**
 * Editable in-memory shape for an agent profile being created or edited
 * in the settings UI. Mirrors the canonical (camelCase) `AgentProfile`
 * with form-state extras. The save helpers translate this back to
 * snake_case at the API boundary.
 *
 * `allow_indexing` is kept as a snake_case form key so the permissions
 * map (which is keyed by snake_case agent metadata) flows through the
 * draft unchanged.
 */
export type DraftProfile = AgentProfile & {
  allow_indexing?: boolean;
  auto_approve?: boolean;
  isNew?: boolean;
  mcp_config?: DraftMcpConfig;
};

export type DraftAgent = Omit<Agent, "profiles"> & { profiles: DraftProfile[]; isNew?: boolean };

function dynamicProfilePayload(profile: DraftProfile) {
  if (profile.kind !== "dynamic" && !profile.dynamic) return undefined;
  return toAgentProfilePayload(profile).dynamic;
}

export const parseProfileMcpServers = (raw: string): Record<string, McpServerDef> => {
  if (!raw.trim()) return {};
  const parsed = JSON.parse(raw) as unknown;
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error(t("agents:mcpConfigMustBeJsonObject"));
  }
  if (MCP_SERVERS_KEY in parsed) {
    const nested = (parsed as { mcpServers?: unknown }).mcpServers;
    if (!nested || typeof nested !== "object" || Array.isArray(nested)) {
      throw new Error(t("agents:mcpKeyMustBeJsonObject", { key: MCP_SERVERS_KEY }));
    }
    return nested as Record<string, McpServerDef>;
  }
  return parsed as Record<string, McpServerDef>;
};

type SaveMcpForProfileParams = {
  draftProfile: DraftProfile;
  targetProfileId: string;
  onToastError: (error: unknown) => void;
};

async function saveMcpForProfile({
  draftProfile,
  targetProfileId,
  onToastError,
}: SaveMcpForProfileParams) {
  if (!draftProfile.mcp_config?.dirty || !draftProfile.mcp_config.servers.trim()) return;
  try {
    const servers = parseProfileMcpServers(draftProfile.mcp_config.servers);
    await updateAgentProfileMcpConfigAction(targetProfileId, {
      enabled: draftProfile.mcp_config.enabled,
      mcpServers: servers,
    });
  } catch (error) {
    onToastError(error);
    throw error;
  }
}

function correlateCreatedProfiles(
  submitted: DraftProfile[],
  created: AgentProfile[],
): Map<string, string> {
  const mappings = new Map<string, string>();
  if (submitted.length === created.length) {
    submitted.forEach((profile, index) => mappings.set(profile.id, created[index].id));
    return mappings;
  }
  for (const profile of submitted) {
    const match = created.find((candidate) => candidate.name === profile.name);
    if (match) mappings.set(profile.id, match.id);
  }
  return mappings;
}

async function saveMcpForCreatedProfiles(
  draftAgent: DraftAgent,
  created: Agent,
  onToastError: (error: unknown) => void,
) {
  if (created.profiles.length === draftAgent.profiles.length) {
    for (let index = 0; index < draftAgent.profiles.length; index += 1) {
      await saveMcpForProfile({
        draftProfile: draftAgent.profiles[index],
        targetProfileId: created.profiles[index].id,
        onToastError,
      });
    }
    return;
  }
  for (const draftProfile of draftAgent.profiles) {
    const createdProfile = created.profiles.find((profile) => profile.name === draftProfile.name);
    if (!createdProfile) continue;
    await saveMcpForProfile({
      draftProfile,
      targetProfileId: createdProfile.id,
      onToastError,
    });
  }
}

function preservePendingMcpDrafts(draftAgent: DraftAgent, created: Agent): Agent {
  const submittedById = new Map<string, DraftProfile>(
    draftAgent.profiles.map((profile) => [profile.id, profile]),
  );
  const profileIds = correlateCreatedProfiles(draftAgent.profiles, created.profiles);
  const submittedByCreatedId = new Map(
    [...profileIds].map(([submittedId, createdId]) => [createdId, submittedById.get(submittedId)]),
  );
  return {
    ...created,
    profiles: created.profiles.map((profile) => {
      const pending = submittedByCreatedId.get(profile.id)?.mcp_config;
      return pending ? { ...profile, mcp_config: pending } : profile;
    }),
  };
}

export type EnsureProfilesFn = (
  agent: DraftAgent,
  displayName: string,
  defaultModel: string,
  permissions?: Record<string, PermissionSetting>,
) => DraftAgent;

export type CloneAgentFn = (agent: Agent) => DraftAgent;

export type SaveAgentCallbacks = {
  onToastError: (error: unknown) => void;
  currentAgentModelConfig: ModelConfig;
  permissionSettings: Record<string, PermissionSetting>;
  resolveDisplayName: (name: string) => string;
  upsertAgent: (agent: Agent, creation?: AgentCreationPublication) => Agent | void;
  setDraftAgent: (agent: DraftAgent | ((current: DraftAgent) => DraftAgent)) => void;
  ensureProfiles: EnsureProfilesFn;
  cloneAgent: CloneAgentFn;
  replaceRoute: (path: string) => void;
};

function buildCreateProfilePayload(profile: DraftProfile) {
  return {
    name: profile.name,
    model: profile.model,
    kind: profile.kind,
    fallback_model: profile.fallbackModel ?? "",
    auto_fallback: profile.autoFallback ?? false,
    mode: profile.mode,
    config_options: profile.configOptions ?? {},
    ...permissionsToProfilePatch(profile),
    cli_passthrough: profile.cliPassthrough ?? false,
    cursor_mcp_auth_enabled: profile.cursorMcpAuthEnabled ?? true,
    cursor_plugins_mcp_enabled: profile.cursorPluginsMcpEnabled ?? true,
    mcp_selection_mode: profile.mcpSelectionMode ?? "inherit",
    mcp_selected_servers: profile.mcpSelectedServers ?? [],
    cli_flags: profile.cliFlags ?? [],
    command_prefix: profile.commandPrefix ?? "",
    ...providerPayloadFields(profile),
    env_vars: profile.envVars ?? [],
    dynamic: dynamicProfilePayload(profile),
  };
}

function changedCursorPreferences(profile: DraftProfile, savedProfile: AgentProfile) {
  const mcpAuth =
    (profile.cursorMcpAuthEnabled ?? true) === (savedProfile.cursorMcpAuthEnabled ?? true)
      ? undefined
      : (profile.cursorMcpAuthEnabled ?? true);
  const pluginsMcp =
    (profile.cursorPluginsMcpEnabled ?? true) === (savedProfile.cursorPluginsMcpEnabled ?? true)
      ? undefined
      : (profile.cursorPluginsMcpEnabled ?? true);
  const selectionMode =
    (profile.mcpSelectionMode ?? "inherit") === (savedProfile.mcpSelectionMode ?? "inherit")
      ? undefined
      : (profile.mcpSelectionMode ?? "inherit");
  const selectedServers = areSelectedServerIdsEqual(
    profile.mcpSelectedServers,
    savedProfile.mcpSelectedServers,
  )
    ? undefined
    : (profile.mcpSelectedServers ?? []);
  return {
    cursor_mcp_auth_enabled: mcpAuth,
    cursor_plugins_mcp_enabled: pluginsMcp,
    mcp_selection_mode: selectionMode,
    mcp_selected_servers: selectedServers,
  };
}

function areSelectedServerIdsEqual(left: string[] = [], right: string[] = []): boolean {
  if (left.length !== right.length) return false;
  const sortedLeft = [...left].sort();
  const sortedRight = [...right].sort();
  return sortedLeft.every((serverId, index) => serverId === sortedRight[index]);
}

function buildUpdateProfilePayload(profile: DraftProfile, savedProfile: AgentProfile) {
  return {
    name: profile.name,
    model: profile.model,
    kind: profile.kind,
    fallback_model: profile.fallbackModel ?? "",
    auto_fallback: profile.autoFallback ?? false,
    mode: profile.mode,
    config_options: profile.configOptions ?? {},
    ...permissionsToProfilePatch(profile),
    cli_passthrough: profile.cliPassthrough ?? false,
    ...changedCursorPreferences(profile, savedProfile),
    cli_flags: profile.cliFlags ?? [],
    command_prefix: profile.commandPrefix ?? "",
    ...providerPayloadFields(profile),
    env_vars: profile.envVars ?? [],
    dynamic: dynamicProfilePayload(profile),
  };
}

export async function saveNewAgent(draftAgent: DraftAgent, callbacks: SaveAgentCallbacks) {
  let created = await createAgentAction({
    name: draftAgent.name,
    workspace_id: draftAgent.workspace_id,
    profiles: draftAgent.profiles.map(buildCreateProfilePayload),
  });

  try {
    await saveMcpForCreatedProfiles(draftAgent, created, callbacks.onToastError);
  } catch (error) {
    const reconciled = preservePendingMcpDrafts(draftAgent, created);
