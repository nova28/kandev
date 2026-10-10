import { useAppStore, useAppStoreApi } from "@/components/state-provider";
import { insertFirstInAgentGroup } from "@/lib/settings/agent-profile-order";
import {
  orderProfilesForSelection,
  toSelectorProfileOptions,
} from "@/lib/settings/agent-profile-selector-order";
import { syncSavedAgentToStore } from "@/app/settings/agents/[agentId]/agent-save-store-sync";
import { parseTurnTimestamp } from "@/lib/state/slices/session/turn-actions";
import { wasSettingsAgentRemoved } from "@/lib/state/settings-agent-removals";
import type { Agent, AgentProfile } from "@/lib/types/http";

export type AgentCreationPublication = {
  profiles: AgentProfile[];
