package compat

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Size caps for operator-supplied compat data. The fragments reach upstream
// requests, so an unbounded blob is an amplification and misconfiguration risk.
const (
	MaxCompatFragmentBytes = 16 * 1024
	MaxCompatHeaders       = 32
	MaxHeaderValueBytes    = 4 * 1024
	MaxCompatExtraBodyKeys = 64
)

// Validation sentinels. The HTTP/CLI layers map these to i18n messages; the
// package itself stays free of the presentation layer.
var (
	ErrCompatTooLarge        = errors.New("compat fragment is too large")
	ErrCompatUnknownKey      = errors.New("unknown compat key")
	ErrCompatForbiddenHeader = errors.New("forbidden compat header")
	ErrCompatInvalidShape    = errors.New("invalid compat fragment")
)

// allowedCompatKeys is the closed set of compat fragment keys. An unknown key is
// rejected rather than silently ignored: a typo would otherwise look like a
// working declaration.
var allowedCompatKeys = map[string]bool{
	"system_as_content":       true,
	"max_tokens_field":        true,
	"supports_developer_role": true,
	"supports_store":          true,
	"supports_thinking":       true,
	"strict_tools_disabled":   true,
	"extra_body":              true,
	"headers":                 true,
	"native_chat_path":        true,
	"ollama_options":          true,
	"ollama_think":            true,
	"stream_options":          true,
	"clamp_max_tokens":        true,
	"system_cache_control":    true,
	"tool_prefix_cache":       true,
	"dashscope_passthrough":   true,
	"tool_dialect":            true,
}

// forbiddenCompatHeaders are header names an operator may never inject: they
// carry credentials or rewrite request routing.
var forbiddenCompatHeaders = map[string]bool{
	"authorization": true,
	"host":          true,
}

// Validate checks an operator-supplied compat fragment: size, JSON shape, known
// keys, forbidden headers and header/extra-body caps. A nil/empty fragment is
// valid (it declares nothing).
func Validate(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > MaxCompatFragmentBytes {
		return fmt.Errorf("%w: %d bytes (max %d)", ErrCompatTooLarge, len(raw), MaxCompatFragmentBytes)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return fmt.Errorf("%w: %v", ErrCompatInvalidShape, err)
	}
	for key, val := range all {
		if !allowedCompatKeys[key] {
			return fmt.Errorf("%w: %s", ErrCompatUnknownKey, key)
		}
		switch key {
		case "headers":
			var headers map[string]string
			if err := json.Unmarshal(val, &headers); err != nil {
				return fmt.Errorf("%w: headers must be a string map: %v", ErrCompatInvalidShape, err)
			}
			if len(headers) > MaxCompatHeaders {
				return fmt.Errorf("%w: %d headers (max %d)", ErrCompatTooLarge, len(headers), MaxCompatHeaders)
			}
			for name, value := range headers {
				if forbiddenCompatHeaders[strings.ToLower(strings.TrimSpace(name))] {
					return fmt.Errorf("%w: %s", ErrCompatForbiddenHeader, name)
				}
				if len(value) > MaxHeaderValueBytes {
					return fmt.Errorf("%w: header %s is %d bytes (max %d)", ErrCompatTooLarge, name, len(value), MaxHeaderValueBytes)
				}
			}
		case "extra_body":
			var body map[string]any
			if err := json.Unmarshal(val, &body); err != nil {
				return fmt.Errorf("%w: extra_body must be an object: %v", ErrCompatInvalidShape, err)
			}
			if len(body) > MaxCompatExtraBodyKeys {
				return fmt.Errorf("%w: %d extra_body keys (max %d)", ErrCompatTooLarge, len(body), MaxCompatExtraBodyKeys)
			}
		}
	}
	f, err := ParseFragment(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCompatInvalidShape, err)
	}
	if f.ClampMaxTokens != nil && *f.ClampMaxTokens < 1 {
		return fmt.Errorf("%w: clamp_max_tokens must be positive", ErrCompatInvalidShape)
	}
	if f.MaxTokensField != nil {
		switch *f.MaxTokensField {
		case "max_tokens", "max_completion_tokens":
		default:
			return fmt.Errorf("%w: max_tokens_field %q", ErrCompatInvalidShape, *f.MaxTokensField)
		}
	}
	return nil
}
