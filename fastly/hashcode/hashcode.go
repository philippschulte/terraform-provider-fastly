// Package hashcode contains functions for converting strings into hash codes.
package hashcode

import (
	"bytes"
	"fmt"
	"hash/crc32"
)

// String hashes a string to a unique hashcode.
// crc32 returns a uint32, but for our use we need
// and non negative integer. Here we cast to an integer
// and invert it if the result is negative.
func String(s string) int {
	v := int(crc32.ChecksumIEEE([]byte(s)))
	if v >= 0 {
		return v
	}
	if -v >= 0 {
		return -v
	}
	// v == MinInt
	return 0
}

// Strings hashes a list of strings to a unique hashcode.
func Strings(strings []string) (string, error) {
	var buf bytes.Buffer

	for _, s := range strings {
		_, err := buf.WriteString(fmt.Sprintf("%s-", s))
		if err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("%d", String(buf.String())), nil
}
