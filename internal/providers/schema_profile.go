package providers

import "github.com/nextlevelbuilder/goclaw/internal/providers/compat"

// SchemaProfile is the tool-schema normalization profile. It is owned by the
// compat resolver (resolved once per provider/model); this alias keeps the
// existing schema transforms reading the same type.
type SchemaProfile = compat.SchemaProfile

// profileForProvider returns the normalization profile for a provider
// identifier. Resolution lives in the compat resolver so the schema layer is
// part of the resolved compat object.
func profileForProvider(name string) SchemaProfile { return compat.ProfileFor(name) }

// isOpenAIStrict reports whether a provider supports OpenAI strict tool mode.
func isOpenAIStrict(name string) bool { return compat.IsStrictProvider(name) }

// isGeminiName reports whether a provider identifier names a Gemini route.
func isGeminiName(name string) bool { return compat.IsGeminiProvider(name) }
