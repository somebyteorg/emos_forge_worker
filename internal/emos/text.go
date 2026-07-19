package emos

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

func readableResponseBody(body []byte) string {
	return readableResponseText(string(body))
}

func readableResponseText(text string) string {
	text = strings.TrimSpace(text)
	for i := 0; i < 3; i++ {
		next := decodeResponseTextOnce(text)
		if next == "" || next == text {
			break
		}
		text = next
	}
	return strings.TrimSpace(text)
}

func decodeResponseTextOnce(text string) string {
	if decoded := decodeJSONResponseText(text); decoded != "" {
		return decoded
	}
	if decoded := decodeEscapedUnicodeText(text); decoded != "" {
		return decoded
	}
	if decoded := decodePercentEncodedText(text); decoded != "" {
		return decoded
	}
	return ""
}

func decodeJSONResponseText(text string) string {
	if !json.Valid([]byte(text)) {
		return ""
	}
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return ""
	}
	if value == nil {
		return "null"
	}
	if message, ok := value.(string); ok {
		return strings.TrimSpace(message)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func decodeEscapedUnicodeText(text string) string {
	if !strings.Contains(text, `\u`) && !strings.Contains(text, `\U`) {
		return ""
	}
	quoted := `"` + strings.NewReplacer(
		`"`, `\"`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
	).Replace(text) + `"`
	decoded, err := strconv.Unquote(quoted)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(decoded)
}

func decodePercentEncodedText(text string) string {
	if !strings.Contains(text, "%") {
		return ""
	}
	decoded, err := url.QueryUnescape(text)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(decoded)
}
