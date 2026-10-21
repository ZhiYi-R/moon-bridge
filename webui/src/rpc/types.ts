export type Paginated<T> = {
  data: T[];
  total: number;
  limit: number;
  offset: number;
};

export type StatusResponse = {
  uptime: string;
  version: string;
  mode: string;
  provider_count: number;
  route_count: number;
  addr: string;
  timestamp: string;
};

export type Offer = {
  model: string;
  upstream_name?: string;
  priority: number;
  input_price: number;
  output_price: number;
  cache_write: number;
  cache_read: number;
};

export type ProviderSummary = {
  key: string;
  protocol: string;
  offer_count: number;
  base_url: string;
  health_status: string;
};

export type ProviderDetail = Omit<ProviderSummary, "health_status"> & {
  health_status?: string;
  api_key: string;
  version: string;
  user_agent: string;
  offers: Offer[];
  web_search: string;
  web_search_max_uses: number;
};

export type ProviderUpsert = {
  base_url: string;
  api_key: string;
  version?: string;
  protocol?: string;
  user_agent?: string;
};

export type ModelSummary = {
  slug: string;
  display_name?: string;
  context_window: number;
  providers: string[];
};

export type ModelDetail = ModelSummary & {
  description: string;
  max_output_tokens: number;
  input_modalities: string[];
};

export type ModelUpsert = {
  display_name?: string;
  description?: string;
  context_window?: number;
  max_output_tokens?: number;
};

export type RouteSummary = {
  alias: string;
  model: string;
  provider: string;
  display_name?: string;
};

export type RouteDetail = RouteSummary & {
  context_window: number;
};

export type RouteUpsert = {
  model: string;
  provider?: string;
  display_name?: string;
  context_window?: number;
};

export type ChangeRow = {
  ID?: number;
  BatchID?: string;
  Action?: string;
  Resource?: string;
  TargetKey?: string;
  Before?: string;
  After?: string;
  Applied?: boolean;
  Error?: string;
  Revision?: number;
  CreatedAt?: string;
  AppliedAt?: string;
  id?: number;
  change_id?: number;
  action?: string;
  resource?: string;
  target_key?: string;
  target?: string;
  before?: string;
  after?: string;
  created_at?: string;
};

export type StatsSummary = {
  requests: number;
  input_tokens: number;
  output_tokens: number;
  cache_hit_rate: number;
  total_cost: number | null;
  cost_unknown?: boolean;
  unpriced_requests?: number;
  currency?: "CNY";
  duration: string;
};

export type UsageStats = {
  totals: UsageStatsTotals;
  by_model: UsageStatsModelRow[];
};

export type UsageStatsTotals = {
  requests: number;
  input_tokens: number;
  output_tokens: number;
  cache_creation: number;
  cache_read: number;
  cache_hit_rate: number;
  cache_write_rate: number;
  cache_rw_ratio: number;
  total_cost: number | null;
  cost_unknown?: boolean;
  unpriced_requests?: number;
  currency?: "CNY";
  duration: string;
};

export type UsageStatsModelRow = {
  model: string;
  actual_model: string;
  requests: number;
  input_tokens: number;
  output_tokens: number;
  cache_creation: number;
  cache_read: number;
  cache_hit_rate: number;
  cost: number | null;
  avg_cost_per_mtoken: number | null;
  cost_unknown?: boolean;
  unpriced_requests?: number;
};

export type SessionInfo = {
  key: string;
  model?: string;
  created_at: string;
  last_used: string;
};

export type DefaultsSettings = {
  model: string;
  max_tokens: number;
  system_prompt: string;
};

export type WebSearchSettings = {
  support: string;
  max_uses: number;
  tavily_api_key: string;
  firecrawl_api_key: string;
  search_max_rounds: number;
};

export type ValidationResult = {
  valid: boolean;
  errors?: string[];
};

export type ImportResult = {
  changes: Array<{ change_id: number; resource: string; target: string }>;
  count: number;
  message: string;
};

export type MutationAccepted = {
  change_id: number;
  status: string;
  message?: string;
};

export type ApplyResult = {
  status: string;
  message: string;
};

export type ResourceKind =
  | "mode"
  | "trace"
  | "log"
  | "server"
  | "defaults"
  | "model"
  | "provider"
  | "provider_offer"
  | "route"
  | "web_search"
  | "cache"
  | "persistence"
  | "extension"
  | "proxy";

export type ResourceStatus = "saved" | "needsAttention" | "restartRequired";

export type RuntimeImpact = "normal" | "critical";

export type ConfigGraph = {
  revision: string;
  resources: ConfigResource[];
  validation: ValidationState;
  runtime: RuntimeState;
  capabilities: ConfigCapabilities;
};

export type ConfigResource = {
  kind: ResourceKind;
  id: string;
  label: string;
  value: Record<string, unknown>;
  schema: ResourceSchema;
  status: ResourceStatus;
  runtimeImpact: RuntimeImpact;
  hotReloadable: boolean;
  references?: ResourceRef[];
};

export type ResourceSchema = {
  fields: FieldSchema[];
};

export type FieldSchema = {
  path: string;
  type: "string" | "number" | "boolean" | "array" | "object" | string;
  label: string;
  required?: boolean;
  secret?: boolean;
  control?: string;
  enum?: string[];
  hotReloadable: boolean;
  runtimeImpact?: string;
};

export type ResourceRef = {
  kind: ResourceKind;
  id: string;
};

export type ValidationState = {
  valid: boolean;
  errors?: FieldError[];
};

export type RuntimeState = {
  status: string;
  errors?: FieldError[];
  message?: string;
};

export type ConfigCapabilities = {
  autosave: boolean;
  logs: boolean;
};

export type FieldError = {
  resourceKind: ResourceKind | "";
  resourceId: string;
  field?: string;
  code: string;
  message: string;
};

export type PatchRequest = {
  baseRevision: string;
  changes: PatchOp[];
};

export type PatchOp = {
  kind: ResourceKind;
  id: string;
  field: string;
  value: unknown;
};

export type PatchResult =
  | "committed"
  | "restartRequired"
  | "revisionConflict"
  | "validationRejected"
  | "runtimeRejected"
  | "draftRejected";

export type PatchResponse = {
  result: PatchResult;
  revision: string;
  graph?: ConfigGraph;
  errors?: FieldError[];
  rollbackValue?: unknown;
};

export type CreateConfigResourceRequest = {
  baseRevision: string;
  id: string;
  value?: Record<string, unknown>;
};

export type LogEntry = {
  timestamp: string;
  level: string;
  message: string;
  attrs?: Record<string, unknown>;
  raw?: string;
};
