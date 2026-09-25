package brand

import (
	"fmt"
	"math"
	"strconv"
)

// minTextContrast is WCAG 2.x level AA for body text. It's a readability
// floor, not a business setting, so it isn't configurable.
const minTextContrast = 4.5

// contrastProblem says what's wrong when the brand's text colour is hard
// to read on its background. Only text is checked: the app works out the
// colour of words on primary, secondary and a background given without
// text itself (black or white, whichever reads better), so those can't
// fail. Text given without a background is checked against white, which
// is near enough the light surface the app derives from the primary.
func (c Colours) contrastProblem() string {
	if c.Text == "" {
		return ""
	}
	bg := c.Background
	against := "colours.background"
	if bg == "" {
		bg, against = "#FFFFFF", "the default (near-white) background"
	}
	if !hexColour.MatchString(c.Text) || !hexColour.MatchString(bg) {
		return "" // already reported as a format problem
	}
	if r := contrast(c.Text, bg); r < minTextContrast {
		return fmt.Sprintf("colours.text %s on %s has contrast %.1f:1; it needs at least %.1f:1 to be readable (WCAG AA)", c.Text, against, r, minTextContrast)
	}
	return ""
}

// contrast is the WCAG contrast ratio between two #RRGGBB colours, 1 to 21.
func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// luminance is WCAG relative luminance. The same formula is in the app
// (lib/core/brand.dart), which picks readable colours with it.
func luminance(hex string) float64 {
	channel := func(i int) float64 {
		v, _ := strconv.ParseUint(hex[i:i+2], 16, 8)
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(1) + 0.7152*channel(3) + 0.0722*channel(5)
}
