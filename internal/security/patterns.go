package security

import (
	"math"
	"regexp"
	"strings"
	"unicode"
)

// PIIPattern holds a compiled regex for detecting a specific type of PII,
// along with an optional validation function for reducing false positives.
type PIIPattern struct {
	Name     string
	Regex    *regexp.Regexp
	Validate func(match string) bool
}

// CompilePatterns returns the complete set of PII detection patterns.
func CompilePatterns() []*PIIPattern {
	return []*PIIPattern{
		{
			Name:     "EMAIL",
			Regex:    regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`),
			Validate: nil, // regex is sufficient
		},
		{
			Name:     "PHONE",
			Regex:    regexp.MustCompile(`(?:\+[1-9]\d{1,14})|(?:\(?\d{3}\)?[\s.\-]?\d{3}[\s.\-]?\d{4})`),
			Validate: nil,
		},
		{
			Name:     "SSN",
			Regex:    regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
			Validate: validateSSN,
		},
		{
			Name:     "CREDIT_CARD",
			Regex:    regexp.MustCompile(`\b(?:\d[\s\-]?){13,19}\b`),
			Validate: validateCreditCard,
		},
		{
			Name:  "API_KEY",
			Regex: regexp.MustCompile(`(?:sk-[a-zA-Z0-9]{20,})|(?:key-[a-zA-Z0-9]{20,})|(?:AKIA[A-Z0-9]{16})|(?:ghp_[a-zA-Z0-9]{36})|(?:glpat-[a-zA-Z0-9\-]{20,})`),
			Validate: nil,
		},
		{
			Name:     "FILE_PATH",
			Regex:    regexp.MustCompile(`(?:/Users/\w+/)|(?:/home/\w+/)|(?:C:\\Users\\\w+\\)`),
			Validate: nil,
		},
		// High-entropy string detection for generic secrets.
		{
			Name:     "API_KEY",
			Regex:    regexp.MustCompile(`(?:api[_\-]?key|secret|token|password|credential)[\s]*[=:]\s*["']?([a-zA-Z0-9/+_\-]{20,})["']?`),
			Validate: validateHighEntropy,
		},
	}
}

// validateSSN checks that a matched SSN is not an obviously invalid number.
// SSNs cannot start with 000, 666, or 900-999 in the area number,
// and the group/serial portions cannot be all zeros.
func validateSSN(match string) bool {
	if len(match) != 11 {
		return false
	}
	area := match[0:3]
	group := match[4:6]
	serial := match[7:11]

	if area == "000" || area == "666" {
		return false
	}
	if area[0] == '9' {
		return false
	}
	if group == "00" {
		return false
	}
	if serial == "0000" {
		return false
	}
	return true
}

// validateCreditCard strips whitespace and dashes, then checks that the
// remaining digits pass the Luhn algorithm.
func validateCreditCard(match string) bool {
	// Strip spaces and dashes.
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			return r
		}
		return -1
	}, match)

	n := len(cleaned)
	if n < 13 || n > 19 {
		return false
	}

	return luhnCheck(cleaned)
}

// luhnCheck performs the Luhn algorithm on a string of digits.
func luhnCheck(number string) bool {
	sum := 0
	alt := false
	for i := len(number) - 1; i >= 0; i-- {
		d := int(number[i] - '0')
		if d < 0 || d > 9 {
			return false
		}
		if alt {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return sum%10 == 0
}

// validateHighEntropy checks whether a matched string has high Shannon entropy,
// which is a heuristic for secrets and API keys.
func validateHighEntropy(match string) bool {
	return shannonEntropy(match) > 3.5
}

// shannonEntropy computes the Shannon entropy of a string in bits per character.
func shannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}

	freq := make(map[rune]float64)
	for _, r := range s {
		freq[r]++
	}

	length := float64(len([]rune(s)))
	entropy := 0.0
	for _, count := range freq {
		p := count / length
		if p > 0 {
			entropy -= p * math.Log2(p)
		}
	}
	return entropy
}
