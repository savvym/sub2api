package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode"
)

const (
	openAIAutoResetResultCodeSuccess  = "success"
	openAIAutoResetResultCodeNoCredit = "no_credit"
	openAIAutoResetMaxCount           = math.MaxInt32
)

// OpenAIAutoResetCanonicalResponse exposes the validated semantics needed by
// the repository to bind the terminal response to its audit outcome.
type OpenAIAutoResetCanonicalResponse struct {
	Body                  string
	ResultCode            string
	WindowsReset          int
	AvailableCount        int
	AvailableCountKnown   bool
	PostProcessRecorded   bool
	RecoveryPending       bool
	RecoveryDeferred      bool
	AccountStateRecovered bool
	WarningCode           string
}

// openAIAutoResetConsumeResult is the complete durable response contract for
// an automatic reset attempt. It deliberately excludes the upstream response
// code and all credit metadata.
type openAIAutoResetConsumeResult struct {
	ResultCode            string `json:"result_code"`
	WindowsReset          int    `json:"windows_reset"`
	AvailableCount        int    `json:"available_count,omitempty"`
	AvailableCountKnown   bool   `json:"available_count_known,omitempty"`
	PostProcessRecorded   bool   `json:"post_process_recorded,omitempty"`
	RecoveryPending       bool   `json:"recovery_pending,omitempty"`
	RecoveryDeferred      bool   `json:"recovery_deferred,omitempty"`
	AccountStateRecovered bool   `json:"account_state_recovered,omitempty"`
	WarningCode           string `json:"warning_code,omitempty"`
}

// CanonicalizeOpenAIAutoResetResponse validates a response produced by the
// current binary and returns its deterministic durable representation. Legacy
// fields are intentionally rejected here; compatibility is read-only.
func CanonicalizeOpenAIAutoResetResponse(body string) (string, error) {
	response, err := ParseOpenAIAutoResetCanonicalResponse(body)
	if err != nil {
		return "", err
	}
	return response.Body, nil
}

// ParseOpenAIAutoResetCanonicalResponse accepts only the current durable
// schema. Legacy response fields are supported solely by the service replay
// decoder and can never enter a new finalization transaction.
func ParseOpenAIAutoResetCanonicalResponse(body string) (OpenAIAutoResetCanonicalResponse, error) {
	result, err := decodeOpenAIAutoResetConsumeResultJSON([]byte(body), false)
	if err != nil {
		return OpenAIAutoResetCanonicalResponse{}, err
	}
	canonical, err := json.Marshal(result)
	if err != nil {
		return OpenAIAutoResetCanonicalResponse{}, fmt.Errorf("encode OpenAI auto-reset response: %w", err)
	}
	return OpenAIAutoResetCanonicalResponse{
		Body:                  string(canonical),
		ResultCode:            result.ResultCode,
		WindowsReset:          result.WindowsReset,
		AvailableCount:        result.AvailableCount,
		AvailableCountKnown:   result.AvailableCountKnown,
		PostProcessRecorded:   result.PostProcessRecorded,
		RecoveryPending:       result.RecoveryPending,
		RecoveryDeferred:      result.RecoveryDeferred,
		AccountStateRecovered: result.AccountStateRecovered,
		WarningCode:           result.WarningCode,
	}, nil
}

func decodeOpenAIAutoResetConsumeResult(value any) (openAIAutoResetConsumeResult, error) {
	if typed, ok := value.(openAIAutoResetConsumeResult); ok {
		if err := validateOpenAIAutoResetConsumeResult(typed, false); err != nil {
			return openAIAutoResetConsumeResult{}, err
		}
		return typed, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return openAIAutoResetConsumeResult{}, fmt.Errorf("encode OpenAI auto-reset response for validation: %w", err)
	}
	return decodeOpenAIAutoResetConsumeResultJSON(raw, true)
}

func decodeOpenAIAutoResetConsumeResultJSON(raw []byte, allowLegacy bool) (openAIAutoResetConsumeResult, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil {
		return openAIAutoResetConsumeResult{}, invalidOpenAIAutoResetResponse("response must be a JSON object", err)
	}
	if fields == nil {
		return openAIAutoResetConsumeResult{}, invalidOpenAIAutoResetResponse("response must be a JSON object", nil)
	}
	if err := requireOpenAIAutoResetJSONEOF(decoder); err != nil {
		return openAIAutoResetConsumeResult{}, err
	}

	allowed := map[string]struct{}{
		"result_code":             {},
		"windows_reset":           {},
		"available_count":         {},
		"available_count_known":   {},
		"post_process_recorded":   {},
		"recovery_pending":        {},
		"recovery_deferred":       {},
		"account_state_recovered": {},
		"warning_code":            {},
	}
	if allowLegacy {
		allowed["code"] = struct{}{}
	}
	for key := range fields {
		if _, ok := allowed[key]; !ok {
			return openAIAutoResetConsumeResult{}, invalidOpenAIAutoResetResponse("response contains an unknown field", nil)
		}
	}

	var result openAIAutoResetConsumeResult
	canonicalCode, hasCanonicalCode, err := decodeOpenAIAutoResetString(fields, "result_code")
	if err != nil {
		return openAIAutoResetConsumeResult{}, err
	}
	legacyCode, hasLegacyCode, err := decodeOpenAIAutoResetString(fields, "code")
	if err != nil {
		return openAIAutoResetConsumeResult{}, err
	}
	if !hasCanonicalCode && !hasLegacyCode {
		return openAIAutoResetConsumeResult{}, invalidOpenAIAutoResetResponse("result code is required", nil)
	}
	if hasCanonicalCode {
		result.ResultCode, err = normalizeOpenAIAutoResetCanonicalResultCode(canonicalCode)
		if err != nil {
			return openAIAutoResetConsumeResult{}, err
		}
	}
	if hasLegacyCode {
		normalizedLegacy, normalizeErr := normalizeOpenAIAutoResetLegacyResultCode(legacyCode)
		if normalizeErr != nil {
			return openAIAutoResetConsumeResult{}, normalizeErr
		}
		if hasCanonicalCode && result.ResultCode != normalizedLegacy {
			return openAIAutoResetConsumeResult{}, invalidOpenAIAutoResetResponse("canonical and legacy result codes conflict", nil)
		}
		result.ResultCode = normalizedLegacy
	}

	var hasWindowsReset bool
	if result.WindowsReset, hasWindowsReset, err = decodeOpenAIAutoResetInt(fields, "windows_reset"); err != nil {
		return openAIAutoResetConsumeResult{}, err
	}
	if !hasWindowsReset {
		return openAIAutoResetConsumeResult{}, invalidOpenAIAutoResetResponse("windows_reset is required", nil)
	}
	if result.AvailableCount, _, err = decodeOpenAIAutoResetInt(fields, "available_count"); err != nil {
		return openAIAutoResetConsumeResult{}, err
	}
	if result.AvailableCountKnown, _, err = decodeOpenAIAutoResetBool(fields, "available_count_known"); err != nil {
		return openAIAutoResetConsumeResult{}, err
	}
	if result.PostProcessRecorded, _, err = decodeOpenAIAutoResetBool(fields, "post_process_recorded"); err != nil {
		return openAIAutoResetConsumeResult{}, err
	}
	if result.RecoveryPending, _, err = decodeOpenAIAutoResetBool(fields, "recovery_pending"); err != nil {
		return openAIAutoResetConsumeResult{}, err
	}
	if result.RecoveryDeferred, _, err = decodeOpenAIAutoResetBool(fields, "recovery_deferred"); err != nil {
		return openAIAutoResetConsumeResult{}, err
	}
	if result.AccountStateRecovered, _, err = decodeOpenAIAutoResetBool(fields, "account_state_recovered"); err != nil {
		return openAIAutoResetConsumeResult{}, err
	}
	if result.WarningCode, _, err = decodeOpenAIAutoResetString(fields, "warning_code"); err != nil {
		return openAIAutoResetConsumeResult{}, err
	}
	if err := validateOpenAIAutoResetConsumeResult(result, hasLegacyCode); err != nil {
		return openAIAutoResetConsumeResult{}, err
	}
	return result, nil
}

func requireOpenAIAutoResetJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err == io.EOF {
		return nil
	} else if err != nil {
		return invalidOpenAIAutoResetResponse("response contains invalid trailing data", err)
	}
	return invalidOpenAIAutoResetResponse("response contains trailing JSON values", nil)
}

func decodeOpenAIAutoResetString(fields map[string]json.RawMessage, key string) (string, bool, error) {
	raw, ok := fields[key]
	if !ok {
		return "", false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", true, invalidOpenAIAutoResetResponse(key+" must be a string", nil)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, invalidOpenAIAutoResetResponse(key+" must be a string", err)
	}
	return value, true, nil
}

func decodeOpenAIAutoResetInt(fields map[string]json.RawMessage, key string) (int, bool, error) {
	raw, ok := fields[key]
	if !ok {
		return 0, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, true, invalidOpenAIAutoResetResponse(key+" must be an integer", nil)
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, true, invalidOpenAIAutoResetResponse(key+" must be an integer", err)
	}
	return value, true, nil
}

func decodeOpenAIAutoResetBool(fields map[string]json.RawMessage, key string) (bool, bool, error) {
	raw, ok := fields[key]
	if !ok {
		return false, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false, true, invalidOpenAIAutoResetResponse(key+" must be a boolean", nil)
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, true, invalidOpenAIAutoResetResponse(key+" must be a boolean", err)
	}
	return value, true, nil
}

func normalizeOpenAIAutoResetCanonicalResultCode(code string) (string, error) {
	switch code {
	case openAIAutoResetResultCodeSuccess, openAIAutoResetResultCodeNoCredit:
		return code, nil
	default:
		return "", invalidOpenAIAutoResetResponse("result_code is not recognized", nil)
	}
}

func normalizeOpenAIAutoResetLegacyResultCode(code string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "ok", "success", "reconciled_success":
		return openAIAutoResetResultCodeSuccess, nil
	case openAIAutoResetResultCodeNoCredit:
		return openAIAutoResetResultCodeNoCredit, nil
	default:
		return "", invalidOpenAIAutoResetResponse("legacy result code is not recognized", nil)
	}
}

func validateOpenAIAutoResetConsumeResult(result openAIAutoResetConsumeResult, allowIncompleteLegacySuccess bool) error {
	if _, err := normalizeOpenAIAutoResetCanonicalResultCode(result.ResultCode); err != nil {
		return err
	}
	if result.WindowsReset < 0 || result.WindowsReset > openAIAutoResetMaxCount ||
		result.AvailableCount < 0 || result.AvailableCount > openAIAutoResetMaxCount {
		return invalidOpenAIAutoResetResponse("response counts are outside the supported range", nil)
	}
	if !result.AvailableCountKnown && result.AvailableCount != 0 {
		return invalidOpenAIAutoResetResponse("available_count requires available_count_known", nil)
	}
	if result.RecoveryDeferred && (!result.RecoveryPending || result.PostProcessRecorded || result.AccountStateRecovered) {
		return invalidOpenAIAutoResetResponse("deferred recovery flags are inconsistent", nil)
	}
	if result.WarningCode != "" {
		if strings.TrimSpace(result.WarningCode) != result.WarningCode || len(result.WarningCode) > 128 {
			return invalidOpenAIAutoResetResponse("warning_code is invalid", nil)
		}
		for _, char := range result.WarningCode {
			if unicode.IsControl(char) || !validOpenAIAutoResetCodeRune(char) {
				return invalidOpenAIAutoResetResponse("warning_code is invalid", nil)
			}
		}
		if !result.RecoveryPending {
			return invalidOpenAIAutoResetResponse("warning_code requires pending recovery", nil)
		}
	}
	if result.RecoveryPending && result.AccountStateRecovered && result.WarningCode == "" {
		return invalidOpenAIAutoResetResponse("pending recovery flags are inconsistent", nil)
	}
	if result.PostProcessRecorded && !result.AccountStateRecovered && !result.RecoveryPending {
		return invalidOpenAIAutoResetResponse("post-process flags are inconsistent", nil)
	}
	if result.ResultCode == openAIAutoResetResultCodeNoCredit &&
		(result.WindowsReset != 0 || result.AvailableCount != 0 || result.AvailableCountKnown || result.PostProcessRecorded ||
			result.RecoveryPending || result.RecoveryDeferred || result.AccountStateRecovered || result.WarningCode != "") {
		return invalidOpenAIAutoResetResponse("no-credit response contains success-only state", nil)
	}
	if result.ResultCode == openAIAutoResetResultCodeSuccess && !result.RecoveryPending {
		if !allowIncompleteLegacySuccess && (!result.PostProcessRecorded || !result.AccountStateRecovered) {
			return invalidOpenAIAutoResetResponse("completed success response is missing recovered state", nil)
		}
		if result.RecoveryDeferred || result.WarningCode != "" {
			return invalidOpenAIAutoResetResponse("completed success response contains recovery failure state", nil)
		}
	}
	return nil
}

func normalizeOpenAIAutoResetUpstreamResultCode(code string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "ok", openAIAutoResetResultCodeSuccess:
		return openAIAutoResetResultCodeSuccess, nil
	case openAIAutoResetResultCodeNoCredit:
		return openAIAutoResetResultCodeNoCredit, nil
	default:
		return "", invalidOpenAIAutoResetResponse("upstream result code is not recognized", nil)
	}
}

func validOpenAIAutoResetCodeRune(char rune) bool {
	return char >= 'a' && char <= 'z' ||
		char >= 'A' && char <= 'Z' ||
		char >= '0' && char <= '9' ||
		char == '_' || char == '-' || char == '.' || char == ':'
}

func invalidOpenAIAutoResetResponse(reason string, cause error) error {
	if cause != nil {
		return fmt.Errorf("invalid OpenAI auto-reset response: %s: %w", reason, cause)
	}
	return fmt.Errorf("invalid OpenAI auto-reset response: %s", reason)
}
