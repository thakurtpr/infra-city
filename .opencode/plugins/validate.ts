/**
 * validate.ts — InfraCity guardrail plugin (OpenCode pre/post tool hooks).
 *
 * - tool.execute.before: blocks destructive cluster ops and secret exfiltration
 *   unless the user explicitly approved them in the current session message.
 * - session.created: injects the deploy-config + safety reminder at session start.
 *
 * Keep this file dependency-free so it loads in any OpenCode runtime.
 */

type ToolEvent = {
  tool: string;
  args?: Record<string, unknown>;
  command?: string;
};

type Decision = { allow: true } | { allow: false; reason: string };

// Substrings that make a bash/kubectl/helm invocation destructive.
const DESTRUCTIVE = [
  "kubectl delete",
  "kubectl drain",
  "kubectl cordon",
  "helm uninstall",
  "helm rollback",
  "kind delete",
  "docker system prune",
  "rm -rf /",
];

// Patterns suggesting secret-value exfiltration (values, not metadata).
const SECRET_SNIFFS = [
  "kubectl get secret -o yaml",
  "kubectl get secrets -o yaml",
  ".data.",
  "base64 --decode",
  "base64 -d",
];

function containsAny(haystack: string, needles: string[]): string | null {
  const lower = haystack.toLowerCase();
  for (const n of needles) {
    if (lower.includes(n.toLowerCase())) return n;
  }
  return null;
}

function renderCommand(ev: ToolEvent): string {
  const parts: string[] = [];
  if (ev.command) parts.push(String(ev.command));
  if (ev.args) {
    for (const v of Object.values(ev.args)) {
      if (typeof v === "string") parts.push(v);
      else if (Array.isArray(v)) parts.push(v.join(" "));
    }
  }
  return parts.join(" ");
}

export async function onToolExecuteBefore(ev: ToolEvent): Promise<Decision> {
  if (ev.tool !== "bash" && ev.tool !== "exec" && ev.tool !== "shell") {
    return { allow: true };
  }
  const cmd = renderCommand(ev);

  const destructive = containsAny(cmd, DESTRUCTIVE);
  if (destructive) {
    return {
      allow: false,
      reason:
        `Blocked destructive op ("${destructive}"). ` +
        `Per AGENTS.md this needs explicit user approval — ask first, then re-run.`,
    };
  }

  if (cmd.includes("kubectl") && containsAny(cmd, SECRET_SNIFFS)) {
    return {
      allow: false,
      reason:
        "Blocked potential secret-value access. InfraCity policy: secrets are " +
        "metadata-only. If you need key names (not values), use " +
        "`kubectl get secret <name> -o jsonpath='{.data}'` key listing via " +
        "the agent's metadata path instead.",
    };
  }

  return { allow: true };
}

export async function onSessionCreated(): Promise<{ notice: string }> {
  return {
    notice:
      "InfraCity session: conventions in AGENTS.md apply. " +
      "Destructive cluster ops and secret-value reads are blocked by " +
      "plugins/validate.ts — ask the user first. " +
      "Deploy values: .opencode/skills/deploy/deploy-config.md.",
  };
}

// OpenCode plugin wiring: subscribe to lifecycle events.
// (Event names follow the OpenCode plugin API: tool.execute.before, session.created.)
export const subscriptions = {
  "tool.execute.before": onToolExecuteBefore,
  "session.created": onSessionCreated,
};
