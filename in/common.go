package in

import (
	"encoding/json"
)

//jsonEscape returns a JSON safe string for embedding into any existing json
func jsonEscape(inStr string) string {
	b, err := json.Marshal(inStr)
	if err != nil {
		mlog.Errorf("Error converting '%v' to json. %v", inStr, err)
		return inStr // still better to include something instead of nothing
	}
	s := string(b)
	return s[1 : len(s)-1]
}

// NormalizeForID builds a normalized ID from given url or listen address so that
// it can be used in the system as unique identifier for various IDs. The returned
// value will not have any special character apart from alphabets and numbers. All
// other characters will be replaced with hyphen (-) in the result.
func NormalizeForID(addr string) string {
	matches := hostPortRe.FindStringSubmatch(addr)
	if len(matches) < 1 {
		return nonAlphaRe.ReplaceAllString(addr, "-")
	}
	return nonAlphaRe.ReplaceAllString(matches[1], "-")
}
