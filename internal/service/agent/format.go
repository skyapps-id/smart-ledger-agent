package agent

import (
	"strconv"
	"strings"
)

// FormatRupiah memformat angka ke "1.000.000" (titik pemisah ribuan).
func FormatRupiah(n float64) string {
	rounded := int64(n)
	s := strconv.FormatInt(rounded, 10)
	return insertThousands(s)
}

func insertThousands(s string) string {
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(ch)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
