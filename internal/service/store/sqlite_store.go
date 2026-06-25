package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"moonbridge/internal/config"
	"moonbridge/internal/db"
)

// SQLiteConfigStore implements ConfigStore backed by a SQLite database.
type SQLiteConfigStore struct {
	db             db.Store
	logger         *slog.Logger
	applyMu        sync.Mutex
	extensionSpecs []config.ExtensionConfigSpec
}

const graphRevisionSettingKey = "__graph_revision"

// NewSQLiteStore creates a new SQLiteConfigStore.
func NewSQLiteStore(s db.Store, logger *slog.Logger, extensionSpecs ...config.ExtensionConfigSpec) *SQLiteConfigStore {
	return &SQLiteConfigStore{db: s, logger: logger, extensionSpecs: extensionSpecs}
}

// Compile-time interface check.
var _ ConfigStore = (*SQLiteConfigStore)(nil)

// --- helpers ---

func (s *SQLiteConfigStore) table(name string) string {
	t, err := s.db.Table(name)
	if err != nil {
		panic(fmt.Sprintf("table %q not registered: %v", name, err))
	}
	return t
}

func nowStr() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// --- SeedFromConfig ---

// SeedFromConfig populates the main tables from a config.Config.
// Clears existing data then writes settings, models, providers+offers, and routes.
func (s *SQLiteConfigStore) SeedFromConfig(cfg *config.Config) error {
	fc := cfg.ToFileConfig()
	return s.db.WithTx(context.Background(), func(tx db.Tx) error {
		revision, err := nextRevisionTx(context.Background(), tx)
		if err != nil {
			return err
		}
		return replaceConfigTx(context.Background(), tx, fc, revision)
	})
}

func (s *SQLiteConfigStore) SaveConfig(ctx context.Context, cfg *config.Config) (string, error) {
	if cfg == nil {
		return "", errors.New("save config: config is nil")
	}
	if err := cfg.Validate(); err != nil {
		return "", fmt.Errorf("save config: validate: %w", err)
	}
	fc := cfg.ToFileConfig()

	var revision string
	if err := s.db.WithTx(ctx, func(tx db.Tx) error {
		next, err := nextRevisionTx(ctx, tx)
		if err != nil {
			return err
		}
		if err := replaceConfigTx(ctx, tx, fc, next); err != nil {
			return err
		}
		revision = next
		return nil
	}); err != nil {
		return "", fmt.Errorf("save config: %w", err)
	}
	return revision, nil
}

func (s *SQLiteConfigStore) CurrentRevision() (string, error) {
	settingsTable := s.table("settings")
	var revision string
	if err := s.db.QueryRowContext(context.Background(),
		"SELECT value FROM "+settingsTable+" WHERE key = ?", graphRevisionSettingKey).Scan(&revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errors.New("current revision: graph revision is not initialized")
		}
		return "", fmt.Errorf("current revision: %w", err)
	}
	if revision == "" {
		return "", errors.New("current revision: graph revision is empty")
	}
	return revision, nil
}

// LoadWebSearchProbes reads all cached web_search probe verdicts.
func (s *SQLiteConfigStore) LoadWebSearchProbes() (map[string]WebSearchProbeRow, error) {
	table := s.table("websearch_probe")
	rows, err := s.db.QueryContext(context.Background(),
		"SELECT candidate_key, supported, fingerprint, probed_at FROM "+table)
	if err != nil {
		return nil, fmt.Errorf("query websearch_probe: %w", err)
	}
	defer rows.Close()
	out := make(map[string]WebSearchProbeRow)
	for rows.Next() {
		var (
			row       WebSearchProbeRow
			supported int
		)
		if err := rows.Scan(&row.CandidateKey, &supported, &row.Fingerprint, &row.ProbedAt); err != nil {
			return nil, fmt.Errorf("scan websearch_probe: %w", err)
		}
		row.Supported = supported != 0
		out[row.CandidateKey] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("websearch_probe rows: %w", err)
	}
	return out, nil
}

// SaveWebSearchProbe upserts a single web_search probe verdict.
func (s *SQLiteConfigStore) SaveWebSearchProbe(row WebSearchProbeRow) error {
	table := s.table("websearch_probe")
	supported := 0
	if row.Supported {
		supported = 1
	}
	probedAt := row.ProbedAt
	if probedAt == "" {
		probedAt = nowStr()
	}
	if _, err := s.db.ExecContext(context.Background(),
		"INSERT OR REPLACE INTO "+table+" (candidate_key, supported, fingerprint, probed_at) VALUES (?, ?, ?, ?)",
		row.CandidateKey, supported, row.Fingerprint, probedAt); err != nil {
		return fmt.Errorf("upsert websearch_probe %s: %w", row.CandidateKey, err)
	}
	return nil
}

func replaceConfigTx(ctx context.Context, tx db.Tx, fc config.FileConfig, revision string) error {
	ts := nowStr()
	providersTable, err := tx.Table("providers")
	if err != nil {
		return err
	}
	offersTable, err := tx.Table("offers")
	if err != nil {
		return err
	}
	modelsTable, err := tx.Table("models")
	if err != nil {
		return err
	}
	routesTable, err := tx.Table("routes")
	if err != nil {
		return err
	}
	settingsTable, err := tx.Table("settings")
	if err != nil {
		return err
	}

	for _, tbl := range []string{providersTable, offersTable, modelsTable, routesTable, settingsTable} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+tbl); err != nil {
			return fmt.Errorf("clear %s: %w", tbl, err)
		}
	}

	settings := buildSettings(fc)
	settings[graphRevisionSettingKey] = revision
	for key, value := range settings {
		if _, err := tx.ExecContext(ctx,
			"INSERT OR REPLACE INTO "+settingsTable+" (key, value) VALUES (?, ?)", key, value); err != nil {
			return fmt.Errorf("insert setting %s: %w", key, err)
		}
	}

	for slug, m := range fc.Models {
		metaJSON, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("marshal model %s: %w", slug, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT OR REPLACE INTO "+modelsTable+" (slug, metadata, created_at, updated_at) VALUES (?, ?, ?, ?)",
			slug, string(metaJSON), ts, ts); err != nil {
			return fmt.Errorf("insert model %s: %w", slug, err)
		}
	}

	for key, p := range fc.Providers {
		wsJSON, err := json.Marshal(p.WebSearch)
		if err != nil {
			return fmt.Errorf("marshal provider %s web_search: %w", key, err)
		}
		extJSON, err := json.Marshal(p.Extensions)
		if err != nil {
			return fmt.Errorf("marshal provider %s extensions: %w", key, err)
		}

		if _, err := tx.ExecContext(ctx,
			"INSERT OR REPLACE INTO "+providersTable+
				" (key, base_url, api_key, version, protocol, enabled, user_agent, web_search, extensions, created_at, updated_at)"+
				" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			key, p.BaseURL, p.APIKey, p.Version, p.Protocol, 1, p.UserAgent,
			string(wsJSON), string(extJSON), ts, ts); err != nil {
			return fmt.Errorf("insert provider %s: %w", key, err)
		}

		for _, offer := range p.Offers {
			var overridesJSON string
			if offer.Overrides != nil {
				b, err := json.Marshal(*offer.Overrides)
				if err != nil {
					return fmt.Errorf("marshal offer %s/%s overrides: %w", key, offer.Model, err)
				}
				overridesJSON = string(b)
			}
			if _, err := tx.ExecContext(ctx,
				"INSERT OR REPLACE INTO "+offersTable+
					" (provider_key, model_slug, upstream_name, priority, input_price, output_price, cache_write, cache_read, overrides)"+
					" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
				key, offer.Model, offer.UpstreamName, offer.Priority,
				offer.Pricing.InputPrice, offer.Pricing.OutputPrice,
				offer.Pricing.CacheWritePrice, offer.Pricing.CacheReadPrice,
				overridesJSON); err != nil {
				return fmt.Errorf("insert offer %s/%s: %w", key, offer.Model, err)
			}
		}
	}

	for alias, r := range fc.Routes {
		extJSON, err := json.Marshal(r.Extensions)
		if err != nil {
			return fmt.Errorf("marshal route %s extensions: %w", alias, err)
		}
		wsJSON, err := json.Marshal(r.WebSearch)
		if err != nil {
			return fmt.Errorf("marshal route %s web_search: %w", alias, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT OR REPLACE INTO "+routesTable+
				" (alias, model_slug, provider_key, display_name, context_window, max_output_tokens, extensions, web_search, created_at, updated_at)"+
				" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			alias, r.Model, r.Provider, r.DisplayName, r.ContextWindow, 0,
			string(extJSON), string(wsJSON), ts, ts); err != nil {
			return fmt.Errorf("insert route %s: %w", alias, err)
		}
	}

	return nil
}

func nextRevisionTx(ctx context.Context, tx db.Tx) (string, error) {
	settingsTable, err := tx.Table("settings")
	if err != nil {
		return "", err
	}
	var current string
	err = tx.QueryRowContext(ctx, "SELECT value FROM "+settingsTable+" WHERE key = ?", graphRevisionSettingKey).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("read current revision: %w", err)
	}

	next := time.Now().UTC().UnixNano()
	if current != "" {
		currentValue, err := strconv.ParseInt(current, 10, 64)
		if err != nil {
			return "", fmt.Errorf("parse current revision %q: %w", current, err)
		}
		if currentValue == math.MaxInt64 {
			return "", errors.New("current revision has reached max int64")
		}
		if next <= currentValue {
			next = currentValue + 1
		}
	}
	return strconv.FormatInt(next, 10), nil
}

// toModelDefFileConfig converts a ModelDef to ModelDefFileConfig for storage.
func toModelDefFileConfig(def config.ModelDef) config.ModelDefFileConfig {
	var reasoningPresets []config.ReasoningLevelPresetFileConfig
	for _, p := range def.SupportedReasoningLevels {
		reasoningPresets = append(reasoningPresets, config.ReasoningLevelPresetFileConfig{
			Effort:      p.Effort,
			Description: p.Description,
		})
	}
	m := config.ModelDefFileConfig{
		ContextWindow:            def.ContextWindow,
		MaxOutputTokens:          def.MaxOutputTokens,
		DisplayName:              def.DisplayName,
		Description:              def.Description,
		BaseInstructions:         def.BaseInstructions,
		DefaultReasoningLevel:    def.DefaultReasoningLevel,
		SupportedReasoningLevels: reasoningPresets,
		DefaultReasoningSummary:  def.DefaultReasoningSummary,
		InputModalities:          def.InputModalities,
	}
	m.SupportsReasoning = boolPtr(def.SupportsReasoning)
	if def.SupportsReasoningSummaries {
		m.SupportsReasoningSummaries = boolPtr(true)
	}
	if def.SupportsImageDetailOriginal {
		m.SupportsImageDetailOriginal = boolPtr(true)
	}
	if len(def.Extensions) > 0 {
		m.Extensions = make(map[string]config.ExtensionFileConfig, len(def.Extensions))
		for name, s := range def.Extensions {
			m.Extensions[name] = config.ExtensionFileConfig{
				Enabled: s.Enabled,
				Config:  cloneMap(s.RawConfig),
			}
		}
	}
	if def.WebSearch.Support != "" || def.WebSearch.MaxUses > 0 || def.WebSearch.TavilyAPIKey != "" || def.WebSearch.FirecrawlAPIKey != "" || def.WebSearch.SearchMaxRounds > 0 {
		m.WebSearch = config.WebSearchFileConfig{
			Support:         string(def.WebSearch.Support),
			MaxUses:         def.WebSearch.MaxUses,
			TavilyAPIKey:    def.WebSearch.TavilyAPIKey,
			FirecrawlAPIKey: def.WebSearch.FirecrawlAPIKey,
			SearchMaxRounds: def.WebSearch.SearchMaxRounds,
		}
	}
	return m
}

func boolPtr(v bool) *bool {
	return &v
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// buildSettings serializes a FileConfig into settings key-value pairs.
func buildSettings(fc config.FileConfig) map[string]string {
	jsonStr := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			return "{}"
		}
		return string(b)
	}
	settings := map[string]string{
		"mode":           fc.Mode,
		"addr":           fc.Server.Addr,
		"auth_token":     fc.Server.AuthToken,
		"max_sessions":   fmt.Sprintf("%d", fc.Server.MaxSessions),
		"session_ttl":    fc.Server.SessionTTL,
		"trace_requests": jsonStr(fc.Trace.Enabled),
		"log_level":      fc.Log.Level,
		"log_format":     fc.Log.Format,
		"defaults":       jsonStr(fc.Defaults),
		"web_search":     jsonStr(fc.WebSearch),
		"cache":          jsonStr(fc.Cache),
		"persistence":    jsonStr(fc.Persistence),
		"proxy":          jsonStr(fc.Proxy),
	}
	if fc.Extensions != nil {
		settings["extensions"] = jsonStr(fc.Extensions)
	}
	return settings
}

// --- LoadAll ---

// LoadAll reads the complete configuration from the main tables.
func (s *SQLiteConfigStore) LoadAll() (*config.Config, error) {
	fc, err := s.loadFileConfig()
	if err != nil {
		return nil, fmt.Errorf("load file config: %w", err)
	}
	if fc.Mode == "" {
		return nil, fmt.Errorf("%w: mode is empty", ErrConfigNotSeeded)
	}
	cfg, err := config.FromFileConfigWithOptions(fc, config.LoadOptions{ExtensionSpecs: s.extensionSpecs})
	if err != nil {
		return nil, fmt.Errorf("convert file config: %w", err)
	}
	return &cfg, nil
}

func (s *SQLiteConfigStore) loadFileConfig() (config.FileConfig, error) {
	fc := config.FileConfig{}

	// Read settings.
	settingsTable := s.table("settings")
	srows, err := s.db.QueryContext(context.Background(), "SELECT key, value FROM "+settingsTable)
	if err != nil {
		return fc, fmt.Errorf("query settings: %w", err)
	}
	defer srows.Close()
	for srows.Next() {
		var key, value string
		if err := srows.Scan(&key, &value); err != nil {
			return fc, fmt.Errorf("scan setting: %w", err)
		}
		applySetting(&fc, key, value)
	}
	if err := srows.Err(); err != nil {
		return fc, fmt.Errorf("settings rows: %w", err)
	}

	// Read models.
	modelsTable := s.table("models")
	mrows, err := s.db.QueryContext(context.Background(), "SELECT slug, metadata FROM "+modelsTable)
	if err != nil {
		return fc, fmt.Errorf("query models: %w", err)
	}
	defer mrows.Close()
	models := make(map[string]config.ModelDefFileConfig)
	for mrows.Next() {
		var slug, metadata string
		if err := mrows.Scan(&slug, &metadata); err != nil {
			return fc, fmt.Errorf("scan model: %w", err)
		}
		var m config.ModelDefFileConfig
		if err := json.Unmarshal([]byte(metadata), &m); err != nil {
			return fc, fmt.Errorf("unmarshal model %s: %w", slug, err)
		}
		models[slug] = m
	}
	if err := mrows.Err(); err != nil {
		return fc, fmt.Errorf("models rows: %w", err)
	}
	if len(models) > 0 {
		fc.Models = models
	}

	// Read providers.
	fc.Providers = make(map[string]config.ProviderDefFileConfig)
	providersTable := s.table("providers")
	prows, err := s.db.QueryContext(context.Background(),
		"SELECT key, base_url, api_key, version, protocol, enabled, user_agent, web_search, extensions FROM "+providersTable)
	if err != nil {
		return fc, fmt.Errorf("query providers: %w", err)
	}
	defer prows.Close()
	for prows.Next() {
		var key, baseURL, apiKey, version, protocol, userAgent string
		var enabled int
		var webSearchStr, extensionsStr sql.NullString
		if err := prows.Scan(&key, &baseURL, &apiKey, &version, &protocol, &enabled, &userAgent, &webSearchStr, &extensionsStr); err != nil {
			return fc, fmt.Errorf("scan provider: %w", err)
		}
		p := config.ProviderDefFileConfig{
			BaseURL:   baseURL,
			APIKey:    apiKey,
			Version:   version,
			Protocol:  protocol,
			UserAgent: userAgent,
		}
		if webSearchStr.Valid && webSearchStr.String != "" && webSearchStr.String != "null" {
			json.Unmarshal([]byte(webSearchStr.String), &p.WebSearch)
		}
		if extensionsStr.Valid && extensionsStr.String != "" && extensionsStr.String != "null" {
			json.Unmarshal([]byte(extensionsStr.String), &p.Extensions)
		}
		fc.Providers[key] = p
	}
	if err := prows.Err(); err != nil {
		return fc, fmt.Errorf("providers rows: %w", err)
	}

	// Read offers and merge into providers.
	offersTable := s.table("offers")
	orows, err := s.db.QueryContext(context.Background(),
		"SELECT provider_key, model_slug, upstream_name, priority, input_price, output_price, cache_write, cache_read, overrides FROM "+offersTable)
	if err != nil {
		return fc, fmt.Errorf("query offers: %w", err)
	}
	defer orows.Close()
	for orows.Next() {
		var providerKey, modelSlug, upstreamName string
		var priority int
		var inputPrice, outputPrice, cacheWrite, cacheRead float64
		var overridesStr sql.NullString
		if err := orows.Scan(&providerKey, &modelSlug, &upstreamName, &priority, &inputPrice, &outputPrice, &cacheWrite, &cacheRead, &overridesStr); err != nil {
			return fc, fmt.Errorf("scan offer: %w", err)
		}
		offer := config.OfferFileConfig{
			Model:        modelSlug,
			UpstreamName: upstreamName,
			Priority:     priority,
			Pricing: config.ModelPricingFileConfig{
				InputPrice:      inputPrice,
				OutputPrice:     outputPrice,
				CacheWritePrice: cacheWrite,
				CacheReadPrice:  cacheRead,
			},
		}
		if overridesStr.Valid && overridesStr.String != "" && overridesStr.String != "null" {
			var m config.ModelDefFileConfig
			if err := json.Unmarshal([]byte(overridesStr.String), &m); err == nil {
				offer.Overrides = &m
			}
		}
		if p, ok := fc.Providers[providerKey]; ok {
			p.Offers = append(p.Offers, offer)
			fc.Providers[providerKey] = p
		} else {
			fc.Providers[providerKey] = config.ProviderDefFileConfig{
				Offers: []config.OfferFileConfig{offer},
			}
		}
	}
	if err := orows.Err(); err != nil {
		return fc, fmt.Errorf("offers rows: %w", err)
	}

	// Read routes.
	routesTable := s.table("routes")
	rrows, err := s.db.QueryContext(context.Background(),
		"SELECT alias, model_slug, provider_key, display_name, context_window, max_output_tokens, extensions, web_search FROM "+routesTable)
	if err != nil {
		return fc, fmt.Errorf("query routes: %w", err)
	}
	defer rrows.Close()
	routeMap := make(map[string]config.RouteFileConfig)
	for rrows.Next() {
		var alias, modelSlug, providerKey, displayName string
		var contextWindow, maxOutputTokens int
		var extensionsStr, webSearchStr sql.NullString
		if err := rrows.Scan(&alias, &modelSlug, &providerKey, &displayName, &contextWindow, &maxOutputTokens, &extensionsStr, &webSearchStr); err != nil {
			return fc, fmt.Errorf("scan route: %w", err)
		}
		r := config.RouteFileConfig{
			Model:         modelSlug,
			Provider:      providerKey,
			DisplayName:   displayName,
			ContextWindow: contextWindow,
		}
		if webSearchStr.Valid && webSearchStr.String != "" && webSearchStr.String != "null" {
			json.Unmarshal([]byte(webSearchStr.String), &r.WebSearch)
		}
		if extensionsStr.Valid && extensionsStr.String != "" && extensionsStr.String != "null" {
			json.Unmarshal([]byte(extensionsStr.String), &r.Extensions)
		}
		routeMap[alias] = r
	}
	if err := rrows.Err(); err != nil {
		return fc, fmt.Errorf("routes rows: %w", err)
	}
	if len(routeMap) > 0 {
		fc.Routes = routeMap
	}
	return fc, nil
}

func applySetting(fc *config.FileConfig, key, value string) {
	switch key {
	case "mode":
		fc.Mode = value
	case "addr":
		fc.Server.Addr = value
	case "auth_token":
		fc.Server.AuthToken = value
	case "max_sessions":
		if v, err := strconv.Atoi(value); err == nil {
			fc.Server.MaxSessions = v
		}
	case "session_ttl":
		fc.Server.SessionTTL = value
	case "trace_requests":
		var enabled bool
		if err := json.Unmarshal([]byte(value), &enabled); err == nil {
			fc.Trace.Enabled = enabled
		}
	case "log_level":
		fc.Log.Level = value
	case "log_format":
		fc.Log.Format = value
	case "defaults":
		var d config.DefaultsFileConfig
		if err := json.Unmarshal([]byte(value), &d); err == nil {
			fc.Defaults = d
		}
	case "web_search":
		var ws config.WebSearchFileConfig
		if err := json.Unmarshal([]byte(value), &ws); err == nil {
			fc.WebSearch = ws
		}
	case "cache":
		var c config.CacheFileConfig
		if err := json.Unmarshal([]byte(value), &c); err == nil {
			fc.Cache = c
		}
	case "persistence":
		var p config.PersistenceFileConfig
		if err := json.Unmarshal([]byte(value), &p); err == nil {
			fc.Persistence = p
		}
	case "proxy":
		var p config.ProxyFileConfig
		if err := json.Unmarshal([]byte(value), &p); err == nil {
			fc.Proxy = p
		}
	case "extensions":
		var e map[string]config.ExtensionFileConfig
		if err := json.Unmarshal([]byte(value), &e); err == nil {
			fc.Extensions = e
		}
	}
}

// --- StageChange ---

func (s *SQLiteConfigStore) StageChange(ch ChangeRow) (int64, error) {
	changesTable := s.table("changes")
	ts := nowStr()
	result, err := s.db.ExecContext(context.Background(),
		"INSERT INTO "+changesTable+" (batch_id, action, resource, target_key, before, after, applied, error, revision, created_at)"+
			" VALUES (?, ?, ?, ?, ?, ?, 0, '', 0, ?)",
		ch.BatchID, ch.Action, ch.Resource, ch.TargetKey, ch.Before, ch.After, ts)
	if err != nil {
		return 0, fmt.Errorf("stage change: %w", err)
	}
	return result.LastInsertId()
}

// --- ListPendingChanges ---

func (s *SQLiteConfigStore) ListPendingChanges() ([]ChangeRow, error) {
	changesTable := s.table("changes")
	rows, err := s.db.QueryContext(context.Background(),
		"SELECT id, batch_id, action, resource, target_key, COALESCE(before,''), COALESCE(after,''), applied, COALESCE(error,''), revision, COALESCE(created_at,''), COALESCE(applied_at,'')"+
			" FROM "+changesTable+" WHERE applied = 0 ORDER BY id ASC")
	if err != nil {
		return nil, fmt.Errorf("list pending changes: %w", err)
	}
	defer rows.Close()
	var changes []ChangeRow
	for rows.Next() {
		var ch ChangeRow
		if err := rows.Scan(&ch.ID, &ch.BatchID, &ch.Action, &ch.Resource, &ch.TargetKey,
			&ch.Before, &ch.After, &ch.Applied, &ch.Error, &ch.Revision, &ch.CreatedAt, &ch.AppliedAt); err != nil {
			return nil, fmt.Errorf("scan change: %w", err)
		}
		changes = append(changes, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return changes, nil
}

// --- ApplyPendingChanges ---

// ApplyPendingChanges applies pending changes atomically:
//  1. Begin transaction, apply changes to main tables, mark applied=1, commit.
//  2. LoadAll and call applier (runtime reload) outside the transaction.
//  3. If applier fails, DB is in a consistent state with changes committed.
func (s *SQLiteConfigStore) ApplyPendingChanges(ctx context.Context, applier ReloadFunc) error {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()

	pending, err := s.ListPendingChanges()
	if err != nil {
		return fmt.Errorf("list pending changes: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}

	// Transaction: apply changes to main tables, mark as applied, commit.
	if err := s.db.WithTx(ctx, func(tx db.Tx) error {
		for _, ch := range pending {
			if err := s.applyChangeTx(ctx, tx, ch); err != nil {
				return fmt.Errorf("change #%d (%s/%s/%s): %w",
					ch.ID, ch.Action, ch.Resource, ch.TargetKey, err)
			}
		}
		changesTable, err := tx.Table("changes")
		if err != nil {
			return err
		}
		ts := nowStr()
		for _, ch := range pending {
			if _, err := tx.ExecContext(ctx,
				"UPDATE "+changesTable+" SET applied = 1, applied_at = ? WHERE id = ?",
				ts, ch.ID); err != nil {
				return fmt.Errorf("mark change #%d: %w", ch.ID, err)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("apply transaction: %w", err)
	}

	// Load config and call applier (outside transaction).
	cfg, err := s.LoadAll()
	if err != nil {
		return fmt.Errorf("load config after apply: %w", err)
	}
	if applier != nil {
		if err := applier(cfg); err != nil {
			return fmt.Errorf("changes applied to DB but applier rejected: %w (DB is consistent)", err)
		}
	}
	return nil
}

// applyChangeTx applies one change row to the main tables within a transaction.
func (s *SQLiteConfigStore) applyChangeTx(ctx context.Context, tx db.Tx, ch ChangeRow) error {
	switch ch.Action {
	case "create", "update":
		var fields map[string]any
		if ch.After != "" {
			if err := json.Unmarshal([]byte(ch.After), &fields); err != nil {
				return fmt.Errorf("unmarshal after: %w", err)
			}
		}
		return s.upsertResourceTx(ctx, tx, ch.Resource, ch.TargetKey, fields)
	case "delete":
		return s.deleteResourceTx(ctx, tx, ch.Resource, ch.TargetKey)
	default:
		return fmt.Errorf("unknown action %q", ch.Action)
	}
}

// upsertResourceTx inserts or updates a resource row.
func (s *SQLiteConfigStore) upsertResourceTx(ctx context.Context, tx db.Tx, resource, key string, fields map[string]any) error {
	ts := nowStr()

	switch resource {
	case "provider":
		table, err := tx.Table("providers")
		if err != nil {
			return err
		}
		var count int
		tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE key = ?", key).Scan(&count)
		if count > 0 {
			setClauses := []string{}
			args := []any{}
			addField(&setClauses, &args, "base_url", fields)
			addField(&setClauses, &args, "api_key", fields)
			addField(&setClauses, &args, "version", fields)
			addField(&setClauses, &args, "protocol", fields)
			addField(&setClauses, &args, "user_agent", fields)
			if v, ok := fields["enabled"]; ok {
				setClauses = append(setClauses, "enabled = ?")
				args = append(args, v)
			}
			if v, ok := fields["web_search"]; ok {
				setClauses = append(setClauses, "web_search = ?")
				args = append(args, v)
			}
			if v, ok := fields["extensions"]; ok {
				setClauses = append(setClauses, "extensions = ?")
				args = append(args, v)
			}
			setClauses = append(setClauses, "updated_at = ?")
			args = append(args, ts)
			args = append(args, key)
			query := "UPDATE " + table + " SET " + strings.Join(setClauses, ", ") + " WHERE key = ?"
			if _, err := tx.ExecContext(ctx, query, args...); err != nil {
				return fmt.Errorf("update provider %s: %w", key, err)
			}
		} else {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO "+table+" (key, base_url, api_key, version, protocol, enabled, user_agent, web_search, extensions, created_at, updated_at)"+
					" VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)",
				key, stringField(fields, "base_url"), stringField(fields, "api_key"),
				stringField(fields, "version"), stringField(fields, "protocol"),
				stringField(fields, "user_agent"), stringField(fields, "web_search"),
				stringField(fields, "extensions"), ts, ts); err != nil {
				return fmt.Errorf("insert provider %s: %w", key, err)
			}
		}

	case "model":
		table, err := tx.Table("models")
		if err != nil {
			return err
		}
		metadata := stringField(fields, "metadata")
		if metadata == "" {
			b, _ := json.Marshal(fields)
			metadata = string(b)
		}
		var count int
		tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE slug = ?", key).Scan(&count)
		if count > 0 {
			if _, err := tx.ExecContext(ctx,
				"UPDATE "+table+" SET metadata = ?, updated_at = ? WHERE slug = ?",
				metadata, ts, key); err != nil {
				return fmt.Errorf("update model %s: %w", key, err)
			}
		} else {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO "+table+" (slug, metadata, created_at, updated_at) VALUES (?, ?, ?, ?)",
				key, metadata, ts, ts); err != nil {
				return fmt.Errorf("insert model %s: %w", key, err)
			}
		}

	case "offer":
		table, err := tx.Table("offers")
		if err != nil {
			return err
		}
		providerKey := stringField(fields, "provider_key")
		modelSlug := stringField(fields, "model_slug")
		var overridesStr string
		if v, ok := fields["overrides"]; ok {
			b, _ := json.Marshal(v)
			overridesStr = string(b)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT OR REPLACE INTO "+table+" (provider_key, model_slug, upstream_name, priority, input_price, output_price, cache_write, cache_read, overrides)"+
				" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			providerKey, modelSlug, stringField(fields, "upstream_name"),
			intField(fields, "priority"), floatField(fields, "input_price"),
			floatField(fields, "output_price"), floatField(fields, "cache_write"),
			floatField(fields, "cache_read"), overridesStr); err != nil {
			return fmt.Errorf("upsert offer: %w", err)
		}

	case "route":
		table, err := tx.Table("routes")
		if err != nil {
			return err
		}
		var count int
		tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE alias = ?", key).Scan(&count)
		if count > 0 {
			setClauses := []string{}
			args := []any{}
			addField(&setClauses, &args, "model_slug", fields)
			addField(&setClauses, &args, "provider_key", fields)
			addField(&setClauses, &args, "display_name", fields)
			if v, ok := fields["context_window"]; ok {
				setClauses = append(setClauses, "context_window = ?")
				args = append(args, v)
			}
			if v, ok := fields["max_output_tokens"]; ok {
				setClauses = append(setClauses, "max_output_tokens = ?")
				args = append(args, v)
			}
			if v, ok := fields["extensions"]; ok {
				setClauses = append(setClauses, "extensions = ?")
				args = append(args, v)
			}
			if v, ok := fields["web_search"]; ok {
				setClauses = append(setClauses, "web_search = ?")
				args = append(args, v)
			}
			setClauses = append(setClauses, "updated_at = ?")
			args = append(args, ts)
			args = append(args, key)
			query := "UPDATE " + table + " SET " + strings.Join(setClauses, ", ") + " WHERE alias = ?"
			if _, err := tx.ExecContext(ctx, query, args...); err != nil {
				return fmt.Errorf("update route %s: %w", key, err)
			}
		} else {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO "+table+" (alias, model_slug, provider_key, display_name, context_window, max_output_tokens, extensions, web_search, created_at, updated_at)"+
					" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
				key, stringField(fields, "model_slug"), stringField(fields, "provider_key"),
				stringField(fields, "display_name"), intField(fields, "context_window"),
				intField(fields, "max_output_tokens"), stringField(fields, "extensions"),
				stringField(fields, "web_search"), ts, ts); err != nil {
				return fmt.Errorf("insert route %s: %w", key, err)
			}
		}

	case "setting":
		table, err := tx.Table("settings")
		if err != nil {
			return err
		}
		b, _ := json.Marshal(fields)
		value := string(b)
		if _, err := tx.ExecContext(ctx,
			"INSERT OR REPLACE INTO "+table+" (key, value) VALUES (?, ?)", key, value); err != nil {
			return fmt.Errorf("upsert setting %s: %w", key, err)
		}

	default:
		return fmt.Errorf("unknown resource %q", resource)
	}
	return nil
}

// deleteResourceTx deletes a resource row.
func (s *SQLiteConfigStore) deleteResourceTx(ctx context.Context, tx db.Tx, resource, key string) error {
	switch resource {
	case "provider":
		table, err := tx.Table("providers")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE key = ?", key); err != nil {
			return err
		}
		// Cascade delete offers.
		offersTable, err := tx.Table("offers")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+offersTable+" WHERE provider_key = ?", key); err != nil {
			return err
		}
	case "model":
		table, err := tx.Table("models")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE slug = ?", key); err != nil {
			return err
		}
	case "offer":
		table, err := tx.Table("offers")
		if err != nil {
			return err
		}
		parts := strings.SplitN(key, "/", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid offer target key %q: expected provider/model", key)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE provider_key = ? AND model_slug = ?", parts[0], parts[1]); err != nil {
			return err
		}
	case "route":
		table, err := tx.Table("routes")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE alias = ?", key); err != nil {
			return err
		}
	case "setting":
		table, err := tx.Table("settings")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE key = ?", key); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown resource %q", resource)
	}
	return nil
}

// --- field helpers ---

func addField(clauses *[]string, args *[]any, name string, fields map[string]any) {
	if v, ok := fields[name]; ok {
		*clauses = append(*clauses, name+" = ?")
		*args = append(*args, v)
	}
}

func stringField(fields map[string]any, key string) string {
	v, ok := fields[key]
	if !ok {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	default:
		b, _ := json.Marshal(val)
		return string(b)
	}
}

func intField(fields map[string]any, key string) int {
	v, ok := fields[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case int64:
		return int(val)
	case json.Number:
		n, _ := val.Int64()
		return int(n)
	default:
		return 0
	}
}

func floatField(fields map[string]any, key string) float64 {
	v, ok := fields[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case json.Number:
		n, _ := val.Float64()
		return n
	default:
		return 0
	}
}

// --- DiscardPendingChanges ---

// DiscardPendingChanges removes all pending (unapplied) changes.
func (s *SQLiteConfigStore) DiscardPendingChanges() error {
	changesTable := s.table("changes")
	_, err := s.db.ExecContext(context.Background(),
		"DELETE FROM "+changesTable+" WHERE applied = 0")
	if err != nil {
		return fmt.Errorf("discard pending changes: %w", err)
	}
	return nil
}

// --- ExportYAML ---

// ExportYAML serializes the current DB state as YAML bytes.
// If includeSecrets is false, API key values are masked.
func (s *SQLiteConfigStore) ExportYAML(includeSecrets bool) ([]byte, error) {
	fc, err := s.loadFileConfig()
	if err != nil {
		return nil, fmt.Errorf("export yaml: load config: %w", err)
	}

	if !includeSecrets {
		maskSecrets(&fc)
	}

	return fc.MarshalYAML()
}

// maskSecrets masks sensitive fields in a FileConfig.
func maskSecrets(fc *config.FileConfig) {
	// Mask provider-level secrets.
	for key, def := range fc.Providers {
		def.APIKey = maskAPIKey(def.APIKey)
		// Mask provider web_search keys and write back to def.WebSearch.
		ws := def.WebSearch
		if ws.TavilyAPIKey != "" {
			ws.TavilyAPIKey = maskAPIKey(ws.TavilyAPIKey)
		}
		if ws.FirecrawlAPIKey != "" {
			ws.FirecrawlAPIKey = maskAPIKey(ws.FirecrawlAPIKey)
		}
		def.WebSearch = ws
		fc.Providers[key] = def
	}

	// Mask global web_search keys.
	ws := fc.WebSearch
	if ws.TavilyAPIKey != "" {
		ws.TavilyAPIKey = maskAPIKey(ws.TavilyAPIKey)
	}
	if ws.FirecrawlAPIKey != "" {
		ws.FirecrawlAPIKey = maskAPIKey(ws.FirecrawlAPIKey)
	}
	fc.WebSearch = ws

	// Mask proxy keys.
	if fc.Proxy.Response.APIKey != "" {
		fc.Proxy.Response.APIKey = maskAPIKey(fc.Proxy.Response.APIKey)
	}
	if fc.Proxy.Anthropic.APIKey != "" {
		fc.Proxy.Anthropic.APIKey = maskAPIKey(fc.Proxy.Anthropic.APIKey)
	}
}

// maskAPIKey masks an API key: first 4 + "****" + last 4.
// If the key is shorter than 8 characters, replaces entirely with "******".
func maskAPIKey(key string) string {
	if len(key) < 8 {
		return "******"
	}
	return key[:4] + "****" + key[len(key)-4:]
}
