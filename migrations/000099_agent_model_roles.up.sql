-- Per-agent model roles: {"<role>": "<provider>/<model>"} (e.g. {"coder":"openai/gpt-5"}).
-- Roles let a request name a workload ("summarizer", "coder") and have it routed to a
-- specific provider/model instead of the agent's primary provider+model. Empty object
-- means "no roles declared" (agent primary wins). Read via store.AgentData.ParseModelRoles.
ALTER TABLE agents
ADD COLUMN IF NOT EXISTS model_roles JSONB NOT NULL DEFAULT '{}'::jsonb;
