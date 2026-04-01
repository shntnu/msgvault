package imessage

import (
	"strings"
	"time"

	"github.com/wesm/msgvault/internal/textimport"
	"howett.net/plist"
)

// appleEpochOffset is the number of seconds between Unix epoch (1970-01-01)
// and Apple/Core Data epoch (2001-01-01).
const appleEpochOffset int64 = 978307200

// appleTimestampToTime converts an Apple epoch timestamp to time.Time.
// macOS High Sierra+ stores dates as nanoseconds since Apple epoch;
// older versions use seconds. We detect the format by checking magnitude.
func appleTimestampToTime(ts int64) time.Time {
	if ts == 0 {
		return time.Time{}
	}
	// Values > 1e12 are nanoseconds (1e12 ns = ~16 minutes from epoch,
	// while 1e12 seconds from epoch = year ~33700).
	if ts > 1_000_000_000_000 {
		sec := ts / 1_000_000_000
		nsec := ts % 1_000_000_000
		return time.Unix(sec+appleEpochOffset, nsec).UTC()
	}
	return time.Unix(ts+appleEpochOffset, 0).UTC()
}

// timeToAppleTimestamp converts a time.Time to an Apple epoch timestamp.
// If useNano is true, returns nanoseconds; otherwise returns seconds.
func timeToAppleTimestamp(t time.Time, useNano bool) int64 {
	appleSec := t.Unix() - appleEpochOffset
	if useNano {
		return appleSec*1_000_000_000 + int64(t.Nanosecond())
	}
	return appleSec
}

// resolveHandle classifies an iMessage handle ID as a phone number,
// email address, or raw identifier (e.g. system handles).
func resolveHandle(handleID string) (phone, email, displayName string) {
	if handleID == "" {
		return "", "", ""
	}
	normalized, err := textimport.NormalizePhone(handleID)
	if err == nil {
		return normalized, "", normalized
	}
	if strings.Contains(handleID, "@") {
		return "", strings.ToLower(handleID), ""
	}
	return "", "", handleID
}

// extractAttributedBodyText decodes an NSKeyedArchiver binary plist blob from
// chat.db's attributedBody column and returns the plain text string.
//
// macOS Ventura+ / iOS 16+ stopped populating the plain-text "text" column for
// most iMessages; the content lives exclusively in attributedBody as an
// NSAttributedString archived via NSKeyedArchiver.
func extractAttributedBodyText(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	var archive struct {
		Top     map[string]plist.UID `plist:"$top"`
		Objects []interface{}        `plist:"$objects"`
	}
	if _, err := plist.Unmarshal(data, &archive); err != nil {
		return ""
	}

	rootUID, ok := archive.Top["root"]
	if !ok || int(rootUID) >= len(archive.Objects) {
		return ""
	}

	rootObj, ok := archive.Objects[rootUID].(map[string]interface{})
	if !ok {
		return ""
	}

	nsStringUID, ok := rootObj["NS.string"].(plist.UID)
	if !ok {
		return ""
	}
	if int(nsStringUID) >= len(archive.Objects) {
		return ""
	}

	text, ok := archive.Objects[nsStringUID].(string)
	if !ok {
		return ""
	}
	return text
}

// snippet returns the first n characters of s, suitable for message preview.
func snippet(s string, maxLen int) string {
	// Normalize whitespace
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen])
	}
	return s
}
