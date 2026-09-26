package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func providersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "providers",
		Short: "Manage LLM providers (requires running gateway)",
	}
	cmd.AddCommand(providersListCmd())
	cmd.AddCommand(providersAddCmd())
	cmd.AddCommand(providersUpdateCmd())
	cmd.AddCommand(providersDeleteCmd())
	cmd.AddCommand(providersVerifyCmd())
	cmd.AddCommand(providersModelsCmd())
	cmd.AddCommand(providersQuirksCmd())
	cmd.AddCommand(providersHealthCmd())
	return cmd
}

// httpProviderFull is a detailed provider representation from the HTTP API.
type httpProviderFull struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ProviderType string `json:"provider_type"`
	BaseURL      string `json:"base_url"`
	Enabled      bool   `json:"enabled"`
	HasAPIKey    bool   `json:"has_api_key"`
}

func providersListCmd() *cobra.Command {
	var jsonOutput bool
	var showModels bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured providers",
		Run: func(cmd *cobra.Command, args []string) {
			requireRunningGatewayHTTP()
			runProvidersList(jsonOutput, showModels)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	cmd.Flags().BoolVar(&showModels, "models", false, "also show available models per provider")
	return cmd
}

func runProvidersList(jsonOutput, showModels bool) {
	providers, err := fetchProviders()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if jsonOutput && !showModels {
		data, _ := json.MarshalIndent(providers, "", "  ")
		fmt.Println(string(data))
		return
	}

	if len(providers) == 0 {
		fmt.Println("No providers configured.")
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "ID\tNAME\tTYPE\tENABLED\n")
	for _, p := range providers {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%v\n", p.ID, p.Name, p.ProviderType, p.Enabled)
	}
	tw.Flush()

	if showModels {
		fmt.Println()
		for _, p := range providers {
			if !p.Enabled {
				continue
			}
			fmt.Printf("── Models for %s (%s) ──\n", p.Name, p.ProviderType)
			resp, err := gatewayHTTPGet("/v1/providers/" + url.PathEscape(p.ID) + "/models")
			if err != nil {
				fmt.Printf("  Error: %v\n", err)
				continue
			}
			raw, _ := json.Marshal(resp["models"])
			var models []httpProviderModel
			if err := json.Unmarshal(raw, &models); err != nil {
				fmt.Printf("  Error parsing models: %v\n", err)
				continue
			}
			if len(models) == 0 {
				fmt.Println("  (no models available)")
				continue
			}
			for _, m := range models {
				fmt.Printf("  %s\n", m.ID)
			}
			fmt.Println()
		}
	}
}

func providersAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add",
		Short: "Add a new provider (interactive)",
		Run: func(cmd *cobra.Command, args []string) {
			requireRunningGatewayHTTP()
			runProvidersAdd()
		},
	}
}

func runProvidersAdd() {
	fmt.Println("── Add Provider ──")
	fmt.Println()

	// Step 1: Provider type
	typeOptions := []SelectOption[string]{
		{"Anthropic", "anthropic"},
		{"OpenAI", "openai"},
		{"Atlas Cloud", "atlascloud"},
		{"OpenRouter", "openrouter"},
		{"DashScope (Alibaba)", "dashscope"},
		{"OpenAI-compatible", "openai_compat"},
	}
	providerType, err := promptSelect("Provider type", typeOptions, 0)
	if err != nil {
		fmt.Println("Cancelled.")
		return
	}

	// Step 2: Name
	name, err := promptString("Provider name", "", providerType)
	if err != nil {
		fmt.Println("Cancelled.")
		return
	}

	// Step 3: API key
	apiKey, err := promptPassword("API key", "will be encrypted at rest")
	if err != nil || apiKey == "" {
		fmt.Println("Cancelled.")
		return
	}

	// Step 4: Base URL (pre-fill per type, editable)
	defaultURL := defaultBaseURL(providerType)
	baseURL := ""
	if providerType == "openai_compat" || providerType == "atlascloud" {
		baseURL, err = promptString("Base URL", "e.g. https://api.example.com/v1", defaultURL)
		if err != nil {
			fmt.Println("Cancelled.")
			return
		}
	}

	body := map[string]any{
		"name":          name,
		"provider_type": providerType,
		"api_key":       apiKey,
		"enabled":       true,
	}
	if baseURL != "" {
		body["base_url"] = baseURL
	}

	resp, err := gatewayHTTPPost("/v1/providers", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating provider: %v\n", err)
		os.Exit(1)
	}

	providerID, _ := resp["id"].(string)
	fmt.Printf("\nProvider %q (%s) created.\n", name, providerType)

	// Offer to verify
	if providerID != "" {
		verify, err := promptConfirm("Verify connection now?", true)
		if err == nil && verify {
			runProviderVerify(providerID, "")
		}
	}
}

func providersUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update <id>",
		Short: "Update a provider",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			requireRunningGatewayHTTP()
			runProvidersUpdate(args[0])
		},
	}
}

func runProvidersUpdate(providerID string) {
	// Fetch current provider
	resp, err := gatewayHTTPGet("/v1/providers/" + url.PathEscape(providerID))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	currentName, _ := resp["name"].(string)
	currentType, _ := resp["provider_type"].(string)

	fmt.Printf("Updating provider: %s (%s)\n", currentName, currentType)
	fmt.Println("Press Enter to keep current value.")
	fmt.Println()

	name, err := promptString("Name", "", currentName)
	if err != nil {
		fmt.Println("Cancelled.")
		return
	}

	apiKey, err := promptPassword("New API key (leave empty to keep current)", "")
	if err != nil {
		fmt.Println("Cancelled.")
		return
	}

	body := map[string]any{"name": name}
	if apiKey != "" {
		body["api_key"] = apiKey
	}

	_, err = gatewayHTTPPut("/v1/providers/"+url.PathEscape(providerID), body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Provider updated.")
}

func providersDeleteCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a provider",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			requireRunningGatewayHTTP()
			if !force {
				confirmed, err := promptConfirm(fmt.Sprintf("Delete provider %q?", args[0]), false)
				if err != nil || !confirmed {
					fmt.Println("Cancelled.")
					return
				}
			}
			if err := gatewayHTTPDelete("/v1/providers/" + url.PathEscape(args[0])); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Provider %q deleted.\n", args[0])
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation")
	return cmd
}

func providersVerifyCmd() *cobra.Command {
	var modelFlag string
	cmd := &cobra.Command{
		Use:   "verify <id>",
		Short: "Verify provider connectivity (ping) or a specific model",
		Long:  "Without --model: pings the provider (registered + reachable check).\nWith --model: sends a small chat request to validate the model alias.",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			requireRunningGatewayHTTP()
			runProviderVerify(args[0], modelFlag)
		},
	}
	cmd.Flags().StringVar(&modelFlag, "model", "", "model alias to verify (omit for connectivity ping)")
	return cmd
}

func runProviderVerify(providerID, model string) {
	fmt.Print("Verifying provider... ")
	var body any
	if model != "" {
		body = map[string]string{"model": model}
	}
	resp, err := gatewayHTTPPost("/v1/providers/"+url.PathEscape(providerID)+"/verify", body)
	if err != nil {
		fmt.Printf("FAILED\n  %v\n", err)
		return
	}
	if valid, _ := resp["valid"].(bool); valid {
		fmt.Println("OK")
		return
	}
	msg, _ := resp["error"].(string)
	if msg == "" {
		msg = "verification failed"
	}
	fmt.Printf("FAILED\n  %s\n", msg)
}

// defaultBaseURL returns the default API base URL for a provider type.
func defaultBaseURL(providerType string) string {
	switch providerType {
	case "anthropic":
		return "https://api.anthropic.com"
	case "openai":
		return "https://api.openai.com/v1"
	case "atlascloud":
		return "https://api.atlascloud.ai/v1"
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	case "dashscope":
		return "https://dashscope.aliyuncs.com/compatible-mode/v1"
	default:
		return ""
	}
}

// providersModelsCmd inspects a provider's model catalogue. The catalogue lives
// in the gateway (llm_models rows seeded from the bundled snapshot and refreshed
// through discovery), so both subcommands are thin HTTP clients of
// /v1/providers/{id}/models.
func providersModelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "models",
		Short: "Inspect a provider's model catalog",
	}
	cmd.AddCommand(providersModelsListCmd())
	cmd.AddCommand(providersModelsRefreshCmd())
	return cmd
}

// providersModelsRefreshTimeout bounds a forced upstream refresh: the gateway
// applies the provider's own timeout (settings.timeout_sec, default 30s), so the
// client must not give up first.
const providersModelsRefreshTimeout = 90 * time.Second

func providersModelsListCmd() *cobra.Command {
	var (
		providerID string
		jsonOutput bool
		refresh    bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the models of a provider",
		Long: "Prints the provider's catalog as the gateway knows it. The catalog is served from\n" +
			"the gateway cache; --refresh re-fetches it from the upstream first.",
		Run: func(cmd *cobra.Command, args []string) {
			requireRunningGatewayHTTP()
			runProvidersModels(providerID, refresh, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&providerID, "provider", "", "provider id or name (required)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "re-fetch the catalog from the upstream")
	_ = cmd.MarkFlagRequired("provider")
	return cmd
}

func providersModelsRefreshCmd() *cobra.Command {
	var (
		providerID string
		jsonOutput bool
	)
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Refresh a provider's model catalog from its upstream",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			requireRunningGatewayHTTP()
			runProvidersModels(providerID, true, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&providerID, "provider", "", "provider id or name (required)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	_ = cmd.MarkFlagRequired("provider")
	return cmd
}

func runProvidersModels(providerID string, refresh, jsonOutput bool) {
	if providerID == "" {
		fmt.Fprintln(os.Stderr, "Error: --provider is required")
		os.Exit(1)
	}
	path := "/v1/providers/" + url.PathEscape(providerID) + "/models"
	client := httpClient
	if refresh {
		path += "?refresh=true"
		client = &http.Client{Timeout: providersModelsRefreshTimeout}
	}
	resp, err := gatewayGetWithClient(client, path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(data))
		return
	}

	if stale, _ := resp["stale"].(bool); stale {
		class, _ := resp["error_class"].(string)
		message, _ := resp["error"].(string)
		fmt.Fprintf(os.Stderr, "warning: catalog is stale (class=%s): %s\n", class, message)
	}
	if fetched, _ := resp["fetched"].(bool); fetched {
		fmt.Fprintln(os.Stderr, "refreshed from upstream")
	}

	raw, _ := json.Marshal(resp["models"])
	var models []httpProviderModel
	if err := json.Unmarshal(raw, &models); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing models: %v\n", err)
		os.Exit(1)
	}
	if len(models) == 0 {
		fmt.Println("No models known for this provider.")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "ID\tNAME\n")
	for _, m := range models {
		fmt.Fprintf(tw, "%s\t%s\n", m.ID, m.Name)
	}
	tw.Flush()
}

// gatewayGetWithClient is gatewayHTTPGet with a caller-supplied client, for
// endpoints that legitimately outlive the default CLI timeout.
func gatewayGetWithClient(client *http.Client, path string) (map[string]any, error) {
	raw, status, err := gatewayDoRaw(client, http.MethodGet, path, nil, gatewayHTTPResponseLimit)
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, parseHTTPError(raw, status)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("invalid JSON response from gateway: %s", string(raw))
	}
	return result, nil
}

func providersQuirksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quirks",
		Short: "Inspect declared provider compatibility quirks",
	}
	cmd.AddCommand(providersQuirksListCmd())
	return cmd
}

func providersQuirksListCmd() *cobra.Command {
	var (
		wireAPI    string
		jsonOutput bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List declared provider quirks for a wire API",
		Long:  "Prints the declared compatibility quirks and fragments for a wire API.",
		Run: func(cmd *cobra.Command, args []string) {
			requireRunningGatewayHTTP()
			runProvidersQuirksList(wireAPI, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&wireAPI, "wire-api", "", "wire API protocol (required, e.g. openai-completions)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	_ = cmd.MarkFlagRequired("wire-api")
	return cmd
}

func runProvidersQuirksList(wireAPI string, jsonOutput bool) {
	path := "/v1/providers/quirks?wire_api=" + url.QueryEscape(wireAPI)
	resp, err := gatewayHTTPGet(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	quirksRaw, _ := resp["quirks"]
	if jsonOutput {
		data, _ := json.MarshalIndent(quirksRaw, "", "  ")
		fmt.Println(string(data))
		return
	}
	var quirks []struct {
		ID             string          `json:"id"`
		WireAPI        string          `json:"wire_api"`
		EndpointFamily *string         `json:"endpoint_family,omitempty"`
		ModelPattern   *string         `json:"model_pattern,omitempty"`
		Compat         json.RawMessage `json:"compat"`
		CompatKeys     []string        `json:"compat_keys"`
		Note           *string         `json:"note,omitempty"`
		Source         string          `json:"source"`
		Enabled        bool            `json:"enabled"`
	}
	quirksJSON, _ := json.Marshal(quirksRaw)
	_ = json.Unmarshal(quirksJSON, &quirks)

	if len(quirks) == 0 {
		fmt.Printf("No quirks declared for wire API %q.\n", wireAPI)
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "WIRE_API\tFAMILY\tMODEL_PATTERN\tSOURCE\tENABLED\tKEYS\tNOTE\n")
	for _, q := range quirks {
		family := "-"
		if q.EndpointFamily != nil && *q.EndpointFamily != "" {
			family = *q.EndpointFamily
		}
		pattern := "*"
		if q.ModelPattern != nil && *q.ModelPattern != "" {
			pattern = *q.ModelPattern
		}
		note := ""
		if q.Note != nil {
			note = *q.Note
		}
		keys := strings.Join(q.CompatKeys, ",")
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%v\t%s\t%s\n", q.WireAPI, family, pattern, q.Source, q.Enabled, keys, note)
	}
	tw.Flush()
}

// providersHealthCmd inspects and repairs provider health. Health lives in the
// gateway's provider_health table (durable cooldown state), so both subcommands
// are thin HTTP clients of /v1/providers/{id}/health — the CLI never talks to the
// database directly.
func providersHealthCmd() *cobra.Command {
	var (
		jsonOutput bool
		resetRef   string
		probeRef   string
		probeModel string
	)
	cmd := &cobra.Command{
		Use:   "health [id]",
		Short: "Show provider health (cooldown, error classes) or clear a cooldown",
		Long: "Prints the durable health of one provider (positional id) or of every provider.\n" +
			"--json always prints a JSON array of health objects.\n" +
			"--reset <id|name> clears the persisted cooldown/failure state of one provider, the\n" +
			"manual escape hatch when a cooldown outlived the outage that caused it.\n" +
			"--probe <id|name> actively probes the provider (through the gateway's verify path)\n" +
			"and records the result; it is never run automatically.",
		Args: cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			requireRunningGatewayHTTP()
			ref := ""
			if len(args) > 0 {
				ref = args[0]
			}
			switch {
			case resetRef != "":
				runProviderHealthAction(resolveProviderRef(resetRef), map[string]any{"reset": true}, jsonOutput)
			case probeRef != "":
				body := map[string]any{"probe": true}
				if probeModel != "" {
					body["model"] = probeModel
				}
				runProviderHealthAction(resolveProviderRef(probeRef), body, jsonOutput)
			default:
				if err := runProvidersHealth(ref, jsonOutput); err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					os.Exit(1)
				}
			}
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON (always an array)")
	cmd.Flags().StringVar(&resetRef, "reset", "", "clear persisted cooldown/health state for this provider (id or name)")
	cmd.Flags().StringVar(&probeRef, "probe", "", "actively probe this provider (id or name) and record the outcome")
	cmd.Flags().StringVar(&probeModel, "model", "", "model for --probe (default: the provider's default model)")
	return cmd
}

// resolveProviderRef turns a provider reference (uuid or name) into a provider id.
func resolveProviderRef(ref string) string {
	if _, err := uuid.Parse(ref); err == nil {
		return ref
	}
	providers, err := fetchProviders()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	for _, p := range providers {
		if p.Name == ref {
			return p.ID
		}
	}
	fmt.Fprintf(os.Stderr, "Error: no provider with id or name %q\n", ref)
	os.Exit(1)
	return ""
}

// runProvidersHealth prints the durable health of one provider, or of every
// provider when ref is empty. It returns an error when the health surface could
// not be read at all: a gateway too old to serve the route must not look like a
// provider set with nothing to report.
func runProvidersHealth(ref string, jsonOutput bool) error {
	reports := []map[string]any{}
	var listErr error
	if ref == "" {
		providers, err := fetchProviders()
		if err != nil {
			return err
		}
		failures := 0
		for _, p := range providers {
			resp, err := gatewayHTTPGet("/v1/providers/" + url.PathEscape(p.ID) + "/health")
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: %s: %v\n", p.Name, err)
				failures++
				continue
			}
			reports = append(reports, resp)
		}
		if failures > 0 && len(reports) == 0 {
			listErr = fmt.Errorf("provider health unavailable: all %d providers failed", failures)
		}
	} else {
		resp, err := gatewayHTTPGet("/v1/providers/" + url.PathEscape(resolveProviderRef(ref)) + "/health")
		if err != nil {
			return err
		}
		reports = append(reports, resp)
	}

	if jsonOutput {
		// --json always prints an array: an empty result is [], never null, so a
		// pipeline consuming this output cannot be handed a null document.
		data, _ := json.MarshalIndent(reports, "", "  ")
		fmt.Println(string(data))
		return listErr
	}

	if len(reports) == 0 {
		if listErr == nil {
			fmt.Println("No providers configured.")
		}
		return listErr
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "PROVIDER\tSTATE\tFAILURES\tCOOLDOWN_UNTIL\tLAST_ERROR\tERROR_CLASSES\n")
	for _, report := range reports {
		name, _ := report["provider"].(string)
		state := "available"
		if cooling, _ := report["cooling_down"].(bool); cooling {
			state = "cooling-down"
		}
		failures := intFromJSON(report["consecutive_failures"])
		cooldownUntil := "-"
		if until, ok := report["cooldown_until"].(string); ok && until != "" {
			cooldownUntil = until
		}
		lastError := "-"
		if class, ok := report["last_error_class"].(string); ok && class != "" {
			lastError = class
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\n",
			name, state, failures, cooldownUntil, lastError, formatErrorCounts(report["error_counts"]))
	}
	tw.Flush()
	return listErr
}

// runProviderHealthAction posts one reset/probe action and prints the result.
func runProviderHealthAction(providerID string, body map[string]any, jsonOutput bool) {
	resp, err := gatewayHTTPPost("/v1/providers/"+url.PathEscape(providerID)+"/health", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if jsonOutput {
		data, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(data))
		return
	}

	name, _ := resp["provider"].(string)
	if reset, _ := body["reset"].(bool); reset {
		fmt.Printf("Provider %s health reset.\n", name)
	}
	if probe, ok := resp["probe"].(map[string]any); ok {
		if valid, _ := probe["valid"].(bool); valid {
			mode, _ := probe["mode"].(string)
			fmt.Printf("Probe OK (%s): %s\n", mode, name)
		} else {
			msg, _ := probe["error"].(string)
			class, _ := probe["error_class"].(string)
			fmt.Printf("Probe FAILED (%s): %s\n", class, msg)
		}
	}
	if cooling, _ := resp["cooling_down"].(bool); cooling {
		until, _ := resp["cooldown_until"].(string)
		fmt.Printf("Provider %s is cooling down until %s.\n", name, until)
		return
	}
	fmt.Printf("Provider %s is available.\n", name)
}

// intFromJSON reads a numeric JSON field the gateway may have encoded as int or
// float64 (encoding/json decodes into map[string]any).
func intFromJSON(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

// formatErrorCounts renders the error-class histogram ("rate_limit=3,timeout=1").
func formatErrorCounts(v any) string {
	counts, ok := v.(map[string]any)
	if !ok || len(counts) == 0 {
		return "-"
	}
	classes := make([]string, 0, len(counts))
	for class, count := range counts {
		classes = append(classes, fmt.Sprintf("%s=%d", class, intFromJSON(count)))
	}
	sort.Strings(classes)
	return strings.Join(classes, ",")
}
