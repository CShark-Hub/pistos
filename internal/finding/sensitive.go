package finding

// WithheldLine replaces a source line flagged by a sensitive finding wherever pistos would otherwise
// show it (report excerpts and any other renderer that prints code).
const WithheldLine = "[line withheld: possible secret]"

// SensitiveLines maps each repository-relative path to the 1-based lines covered by sensitive
// findings, with the family that flagged each line. Every renderer that prints source must withhold
// these lines, including when they only appear as context of an unrelated issue.
func SensitiveLines(issues []Issue) map[string]map[int]string {
	out := map[string]map[int]string{}
	for _, is := range issues {
		if !is.Sensitive || is.Location.Line <= 0 {
			continue
		}
		lines := out[is.Location.Path]
		if lines == nil {
			lines = map[int]string{}
			out[is.Location.Path] = lines
		}
		for n := is.Location.Line; n <= max(is.Location.Line, is.Location.EndLine); n++ {
			if _, ok := lines[n]; !ok {
				lines[n] = is.Family
			}
		}
	}
	return out
}

// WithholdLines returns a copy of a file's lines (index 0 is line 1) in which every flagged line is
// replaced by placeholder(family).
func WithholdLines(lines []string, flagged map[int]string, placeholder func(family string) string) []string {
	if len(flagged) == 0 {
		return lines
	}
	out := append([]string(nil), lines...)
	for n, family := range flagged {
		if n >= 1 && n <= len(out) {
			out[n-1] = placeholder(family)
		}
	}
	return out
}
