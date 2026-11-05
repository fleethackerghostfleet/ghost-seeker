package command

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	numericDurationPattern = regexp.MustCompile(`(\d+(\.\d+)?)\s*(hours?|hrs?|h|minutes?|mins?|m|days?|d)\b`)
	wordDurationPattern    = regexp.MustCompile(`\b(twenty-four|twelve|eleven|ten|nine|eight|seven|six|five|four|three|two|one|a|an)\s+(hours?|hrs?|h|minutes?|mins?|m|days?|d)\b`)
	durationPhrasePattern  = regexp.MustCompile(`\b(overnight|all day|rest of (the )?watch|this watch|the watch|rest of (the )?day|until further notice|till further notice)\b`)
	lookbackPrefixPattern  = regexp.MustCompile(`\b(last|past)\s+(the\s+)?$`)
	durationPrefixPattern  = regexp.MustCompile(`\b(for\s+next|for\s+(the\s+)?|next)\s*$`)
	durationGapPattern     = regexp.MustCompile(`^\s*,?\s*(and\s*)?$`)
)

type durationSpan struct {
	start      int
	end        int
	minutes    float64
	indefinite bool
}

func extractDuration(text string) (minutes int, rest string) {
	text = strings.ToLower(text)
	spans := make([]durationSpan, 0)
	candidates := make([]durationSpan, 0)
	numeric := make([]durationSpan, 0)

	for _, match := range numericDurationPattern.FindAllStringSubmatchIndex(text, -1) {
		span := makeDurationSpan(text, match)
		if lookbackPrefixPattern.MatchString(text[:span.start]) {
			continue
		}
		spans = append(spans, span)
		numeric = append(numeric, span)
	}
	for _, match := range wordDurationPattern.FindAllStringSubmatchIndex(text, -1) {
		span := makeWordDurationSpan(text, match)
		if lookbackPrefixPattern.MatchString(text[:span.start]) {
			continue
		}
		spans = append(spans, span)
		numeric = append(numeric, span)
	}
	sort.Slice(numeric, func(i, j int) bool { return numeric[i].start < numeric[j].start })
	for _, match := range durationPhrasePattern.FindAllStringIndex(text, -1) {
		phrase := text[match[0]:match[1]]
		span := durationSpan{
			start: match[0], end: match[1], minutes: phraseMinutes(phrase),
			indefinite: phrase == "until further notice" || phrase == "till further notice",
		}
		spans = append(spans, span)
		candidates = append(candidates, span)
	}

	// Numeric and word durations can form one expression, such as "1h30m".
	for i := 0; i < len(numeric); {
		group := numeric[i]
		groupMinutes := group.minutes
		j := i + 1
		for j < len(numeric) && durationGapPattern.MatchString(text[group.end:numeric[j].start]) {
			groupMinutes += numeric[j].minutes
			group.end = numeric[j].end
			j++
		}
		group.minutes = groupMinutes
		candidates = append(candidates, group)
		i = j
	}

	if len(candidates) > 0 {
		winner := candidates[0]
		for _, candidate := range candidates[1:] {
			if candidate.start < winner.start {
				winner = candidate
			}
		}
		if winner.indefinite {
			minutes = 0
		} else {
			switch {
			case winner.minutes < 1:
				minutes = 1
			case winner.minutes > 10080:
				minutes = 10080
			default:
				minutes = int(math.Round(winner.minutes))
				if minutes < 1 {
					minutes = 1
				}
				if minutes > 10080 {
					minutes = 10080
				}
			}
		}
		if prefix := durationPrefixPattern.FindStringIndex(text[:winner.start]); prefix != nil {
			spans = append(spans, durationSpan{start: prefix[0], end: winner.start})
		}
	}

	rest = removeDurationSpans(text, spans)
	return minutes, rest
}

func makeDurationSpan(text string, match []int) durationSpan {
	value, _ := strconv.ParseFloat(text[match[2]:match[3]], 64)
	return durationSpan{start: match[0], end: match[1], minutes: minutesFor(value, text[match[6]:match[7]])}
}

func makeWordDurationSpan(text string, match []int) durationSpan {
	value := map[string]float64{
		"a": 1, "an": 1, "one": 1, "two": 2, "three": 3, "four": 4,
		"five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9,
		"ten": 10, "eleven": 11, "twelve": 12, "twenty-four": 24,
	}[text[match[2]:match[3]]]
	return durationSpan{start: match[0], end: match[1], minutes: minutesFor(value, text[match[4]:match[5]])}
}

func minutesFor(value float64, unit string) float64 {
	switch {
	case strings.HasPrefix(unit, "h"):
		return value * 60
	case strings.HasPrefix(unit, "d"):
		return value * 1440
	default:
		return value
	}
}

func phraseMinutes(phrase string) float64 {
	switch {
	case phrase == "overnight", phrase == "rest of the day", phrase == "rest of day":
		return 720
	case phrase == "all day":
		return 1440
	case phrase == "until further notice", phrase == "till further notice":
		return 0
	default:
		return 240
	}
}

func removeDurationSpans(text string, spans []durationSpan) string {
	if len(spans) == 0 {
		return text
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	var result strings.Builder
	cursor := 0
	for _, span := range spans {
		if span.start < cursor {
			if span.end > cursor {
				cursor = span.end
			}
			continue
		}
		result.WriteString(text[cursor:span.start])
		result.WriteByte(' ')
		cursor = span.end
	}
	result.WriteString(text[cursor:])
	return result.String()
}
