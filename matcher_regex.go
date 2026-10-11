// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

type javaRegexFlags struct{ insensitive, unicodeCase, unicodeClasses bool }

var javaFlagPrefix = regexp.MustCompile(`^\(\?([idmsuxU-]+)([:)])`)

// Translate Java character classes, flags and possessive quantifiers while
// retaining constructs supported by the backtracking fallback engine.
func javaRegex(pattern string) string {
	translated, consumed := javaRegexSegment(pattern, javaRegexFlags{})
	if consumed != len(pattern) {
		return pattern
	}
	return javaPossessive(translated)
}
func javaRegexSegment(pattern string, flags javaRegexFlags) (string, int) {
	var out strings.Builder
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c == ')' {
			return out.String(), i
		}
		if c == '(' {
			if match := javaFlagPrefix.FindStringSubmatch(pattern[i:]); match != nil {
				next := flags
				negative := false
				var preserved strings.Builder
				negativeWritten := false
				for _, flag := range match[1] {
					if flag == '-' {
						negative = true
						continue
					}
					switch flag {
					case 'i':
						next.insensitive = !negative
					case 'u':
						next.unicodeCase = !negative
					case 'U':
						next.unicodeClasses = !negative
						next.unicodeCase = !negative
					case 'd':
					default:
						if negative && !negativeWritten {
							preserved.WriteByte('-')
							negativeWritten = true
						}
						preserved.WriteRune(flag)
					}
				}
				prefixFlags := ""
				if preserved.Len() > 0 {
					prefixFlags = "(?" + preserved.String() + ")"
				}
				i += len(match[0]) - 1
				if match[2] == ":" {
					inside, n := javaRegexSegment(pattern[i+1:], next)
					if i+1+n >= len(pattern) {
						out.WriteString("(?:" + prefixFlags + inside)
					} else {
						out.WriteString("(?:" + prefixFlags + inside + ")")
					}
					i += n + 1
				} else {
					out.WriteString(prefixFlags)
					flags = next
				}
				continue
			}
			prefix := "("
			start := i + 1
			for _, special := range []string{"(?<=", "(?<!", "(?:", "(?=", "(?!", "(?>"} {
				if strings.HasPrefix(pattern[i:], special) {
					prefix = special
					start = i + len(special)
					break
				}
			}
			if prefix == "(" && strings.HasPrefix(pattern[i:], "(?<") {
				if end := strings.IndexByte(pattern[i:], '>'); end >= 0 {
					prefix = pattern[i : i+end+1]
					start = i + end + 1
				}
			}
			inside, n := javaRegexSegment(pattern[start:], flags)
			if start+n >= len(pattern) {
				out.WriteString(prefix + inside)
			} else {
				out.WriteString(prefix + inside + ")")
			}
			i = start + n
			continue
		}
		if c == '[' {
			end := javaClassEnd(pattern, i)
			if end >= 0 {
				out.WriteString(javaClass(pattern[i+1:end], flags))
				i = end
				continue
			}
		}
		if c == '\\' && i+1 < len(pattern) {
			i++
			next := pattern[i]
			if next == 'Q' {
				end := strings.Index(pattern[i+1:], `\E`)
				if end < 0 {
					end = len(pattern) - i - 1
				}
				literal := pattern[i+1 : i+1+end]
				for _, r := range literal {
					text := regexp.QuoteMeta(string(r))
					if flags.insensitive && !flags.unicodeCase && r < 128 && ((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
						text = "[" + string(r) + string(r^32) + "]"
					}
					out.WriteString(text)
				}
				i += end
				if i+2 < len(pattern) && pattern[i+1:i+3] == `\E` {
					i += 2
				}
				continue
			}
			if strings.ContainsRune("pPx", rune(next)) && i+1 < len(pattern) && pattern[i+1] == '{' {
				end := strings.IndexByte(pattern[i+1:], '}')
				if end >= 0 {
					token := pattern[i-1 : i+end+2]
					if flags.insensitive && flags.unicodeCase {
						token = "(?i:" + token + ")"
					}
					out.WriteString(token)
					i += end + 1
					continue
				}
			}
			replacement := javaRegexEscape(next, flags)
			if replacement == "" {
				replacement = "\\" + string(next)
			}
			if flags.insensitive && flags.unicodeCase {
				replacement = "(?i:" + replacement + ")"
			}
			out.WriteString(replacement)
			continue
		}
		if c >= utf8.RuneSelf && flags.insensitive && flags.unicodeCase {
			r, n := utf8.DecodeRuneInString(pattern[i:])
			out.WriteString("(?i:" + string(r) + ")")
			i += n - 1
			continue
		}
		if flags.insensitive && ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			if flags.unicodeCase {
				if c == 'i' || c == 'I' {
					out.WriteString("[iIıİ]")
				} else {
					out.WriteString("(?i:" + string(c) + ")")
				}
			} else {
				out.WriteByte('[')
				out.WriteByte(c)
				out.WriteByte(c ^ 32)
				out.WriteByte(']')
			}
		} else {
			out.WriteByte(c)
		}
	}
	return out.String(), len(pattern)
}
func javaRegexEscape(c byte, flags javaRegexFlags) string {
	if c == 'R' {
		return "(?:\\r\\n|[\\n\\v\\f\\r\u0085\u2028\u2029])"
	}
	if flags.unicodeClasses {
		switch c {
		case 'd':
			return `\p{Nd}`
		case 'D':
			return `\P{Nd}`
		case 'w':
			return `[\p{L}\p{M}\p{N}\p{Pc}\x{200c}\x{200d}]`
		case 'W':
			return `[^\p{L}\p{M}\p{N}\p{Pc}\x{200c}\x{200d}]`
		case 's':
			return `[\p{Z}\t\n\x0B\f\r\x{0085}]`
		case 'S':
			return `[^\p{Z}\t\n\x0B\f\r\x{0085}]`
		}
	} else {
		switch c {
		case 'd':
			return `[0-9]`
		case 'D':
			return `[^0-9]`
		case 'w':
			return `[a-zA-Z_0-9]`
		case 'W':
			return `[^a-zA-Z_0-9]`
		case 's':
			return `[ \t\n\x0B\f\r]`
		case 'S':
			return `[^ \t\n\x0B\f\r]`
		}
	}
	return ""
}
func javaClassEnd(pattern string, start int) int {
	depth := 0
	for i := start; i < len(pattern); i++ {
		if pattern[i] == '\\' {
			i++
			continue
		}
		if pattern[i] == '[' {
			depth++
		}
		if pattern[i] == ']' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
func javaClass(content string, flags javaRegexFlags) string {
	negative := strings.HasPrefix(content, "^")
	if negative {
		content = content[1:]
	}
	var components []string
	start, depth := 0, 0
	for i := 0; i+1 < len(content); i++ {
		if content[i] == '\\' {
			i++
			continue
		}
		if content[i] == '[' {
			depth++
		}
		if content[i] == ']' {
			depth--
		}
		if depth == 0 && content[i:i+2] == "&&" {
			components = append(components, content[start:i])
			start = i + 2
			i++
		}
	}
	if len(components) > 0 {
		components = append(components, content[start:])
		var out strings.Builder
		out.WriteString("(?:")
		for _, part := range components[:len(components)-1] {
			out.WriteString("(?=" + javaClass(part, flags) + ")")
		}
		out.WriteString(javaClass(components[len(components)-1], flags) + ")")
		result := out.String()
		if negative {
			result = "(?:(?!" + result + ")[\\s\\S])"
		}
		return result
	}
	var unions []string
	var simple strings.Builder
	for i := 0; i < len(content); i++ {
		c := content[i]
		if c == '[' {
			end := javaClassEnd(content, i)
			if end >= 0 {
				unions = append(unions, javaClass(content[i+1:end], flags))
				i = end
				continue
			}
		}
		if c == '\\' && i+1 < len(content) {
			i++
			replacement := javaRegexEscape(content[i], flags)
			if strings.HasPrefix(replacement, "[") {
				unions = append(unions, replacement)
			} else {
				simple.WriteByte('\\')
				simple.WriteByte(content[i])
			}
			continue
		}
		simple.WriteByte(c)
	}
	atom := simple.String()
	if flags.insensitive && !flags.unicodeCase {
		var extra strings.Builder
		for i := 0; i < len(atom); i++ {
			c := atom[i]
			if c == '\\' {
				i++
				continue
			}
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				extra.WriteByte(c ^ 32)
				if i+2 < len(atom) && atom[i+1] == '-' {
					extra.WriteByte('-')
					extra.WriteByte(atom[i+2] ^ 32)
					i += 2
				}
			}
		}
		atom += extra.String()
	}
	if atom != "" {
		unions = append(unions, "["+atom+"]")
	}
	result := "(?:" + strings.Join(unions, "|") + ")"
	if len(unions) == 1 {
		result = unions[0]
	}
	if flags.insensitive && flags.unicodeCase {
		result = "(?i:" + result + ")"
	}
	if negative {
		if strings.HasPrefix(result, "[") && strings.HasSuffix(result, "]") {
			result = "[^" + result[1:]
		} else {
			result = "(?:(?!" + result + ")[\\s\\S])"
		}
	}
	return result
}

func javaPossessive(pattern string) string {
	lastAtom := -1
	var groups []int
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			lastAtom = i
			i++
			if i+1 < len(pattern) && strings.ContainsRune("pPx", rune(pattern[i])) && pattern[i+1] == '{' {
				for i++; i < len(pattern) && pattern[i] != '}'; i++ {
				}
			}
		case '[':
			lastAtom = i
			for i++; i < len(pattern); i++ {
				if pattern[i] == '\\' {
					i++
				} else if pattern[i] == ']' {
					break
				}
			}
		case '(':
			groups = append(groups, i)
			lastAtom = -1
			// Group flags and assertions are syntax, not quantifiable atoms.
			if i+1 < len(pattern) && pattern[i+1] == '?' {
				i++
				for i+1 < len(pattern) && strings.ContainsRune(":=!<>imsuxUd-", rune(pattern[i+1])) {
					i++
					if strings.ContainsRune(":=!>", rune(pattern[i])) {
						break
					}
				}
			}
		case ')':
			if len(groups) > 0 {
				lastAtom = groups[len(groups)-1]
				groups = groups[:len(groups)-1]
			}
		case '*', '+', '?', '{':
			end := i
			if pattern[i] == '{' {
				for end < len(pattern) && pattern[end] != '}' {
					end++
				}
				if end == len(pattern) {
					continue
				}
			}
			if lastAtom >= 0 && end+1 < len(pattern) && pattern[end+1] == '+' {
				pattern = pattern[:lastAtom] + "(?>" + pattern[lastAtom:end+1] + ")" + pattern[end+2:]
				i = end + 4
			} else {
				i = end
			}
		case '|', '^', '$':
			lastAtom = -1
		default:
			lastAtom = i
		}
	}
	return pattern
}
