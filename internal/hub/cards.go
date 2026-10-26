package hub

import (
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
)

// themeCard is a Material desktop wearing a colour theme that ships no screenshot: bar, clock, media card
// and sidebar drawn from its own roles, so a card shows what applying it does rather than a swatch.
func themeCard(e *entry) string {
	c := func(role string) string { return orDefault(e.palette[role], "#808080") }
	var toggles, days, workspaces strings.Builder
	for i := 0; i < 6; i++ {
		x, y := 888+(i%2)*176, 188+(i/2)*76
		on := i == 0 || i == 1 || i == 4
		plate, ink, w := c("m3surfaceContainerHigh"), c("m3onSurfaceVariant"), 60
		if on {
			plate, ink, w = c("m3primary"), c("m3onPrimary"), 72
		}
		fmt.Fprintf(&toggles, `<rect x="%d" y="%d" width="168" height="64" rx="22" fill="%s"/>`+
			`<circle cx="%d" cy="%d" r="11" fill="%s"/>`+
			`<rect x="%d" y="%d" width="%d" height="10" rx="5" fill="%s"/>`+
			`<rect x="%d" y="%d" width="44" height="8" rx="4" fill="%s" fill-opacity="0.6"/>`,
			x, y, plate, x+32, y+32, ink, x+56, y+22, w, ink, x+56, y+38, ink)
	}
	for i := 0; i < 21; i++ {
		cx, cy := 922+(i%7)*46, 676+(i/7)*30
		if i == 9 {
			fmt.Fprintf(&days, `<circle cx="%d" cy="%d" r="13" fill="%s"/><circle cx="%d" cy="%d" r="4" fill="%s"/>`,
				cx, cy, c("m3primary"), cx, cy, c("m3onPrimary"))
		} else {
			fmt.Fprintf(&days, `<circle cx="%d" cy="%d" r="4" fill="%s" fill-opacity="0.45"/>`, cx, cy, c("m3onSurfaceVariant"))
		}
	}
	for i := 0; i < 3; i++ {
		fmt.Fprintf(&workspaces, `<circle cx="%d" cy="46" r="5" fill="%s" fill-opacity="0.5"/>`, 150+i*22, c("m3onSurfaceVariant"))
	}
	r := strings.NewReplacer(
		"{toggles}", toggles.String(), "{days}", days.String(), "{workspaces}", workspaces.String(),
		"{name}", html.EscapeString(text(e.item, "name")))
	svg := r.Replace(themeSVG)
	// {m3role} placeholders take the theme's colours.
	var out strings.Builder
	for {
		i := strings.Index(svg, "{m3")
		if i < 0 {
			out.WriteString(svg)
			break
		}
		j := strings.Index(svg[i:], "}")
		out.WriteString(svg[:i])
		out.WriteString(c(svg[i+1 : i+j]))
		svg = svg[i+j+1:]
	}
	return out.String()
}

const themeSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="1280" height="800" viewBox="0 0 1280 800">
<defs>
<linearGradient id="wall" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="{m3tertiaryContainer}"/><stop offset="0.55" stop-color="{m3surfaceContainerLow}"/><stop offset="1" stop-color="{m3background}"/></linearGradient>
<radialGradient id="glowA" cx="0.5" cy="0.5" r="0.5"><stop offset="0" stop-color="{m3primary}" stop-opacity="0.42"/><stop offset="1" stop-color="{m3primary}" stop-opacity="0"/></radialGradient>
<radialGradient id="glowB" cx="0.5" cy="0.5" r="0.5"><stop offset="0" stop-color="{m3tertiary}" stop-opacity="0.30"/><stop offset="1" stop-color="{m3tertiary}" stop-opacity="0"/></radialGradient>
<linearGradient id="art" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="{m3tertiary}"/><stop offset="1" stop-color="{m3primaryContainer}"/></linearGradient>
</defs>
<rect width="1280" height="800" fill="url(#wall)"/>
<circle cx="300" cy="620" r="420" fill="url(#glowA)"/>
<circle cx="760" cy="140" r="360" fill="url(#glowB)"/>
<rect x="24" y="20" width="1232" height="52" rx="26" fill="{m3surfaceContainer}"/>
<circle cx="58" cy="46" r="11" fill="{m3onSurfaceVariant}"/>
<rect x="88" y="36" width="44" height="20" rx="10" fill="{m3primary}"/>
{workspaces}
<rect x="590" y="39" width="100" height="14" rx="7" fill="{m3onSurface}"/>
<circle cx="1146" cy="46" r="7" fill="{m3onSurfaceVariant}"/>
<circle cx="1176" cy="46" r="7" fill="{m3onSurfaceVariant}"/>
<rect x="1198" y="38" width="32" height="16" rx="8" fill="{m3secondaryContainer}"/>
<text x="56" y="300" font-family="Rubik, Roboto Flex, sans-serif" font-size="164" font-weight="600" fill="{m3primary}" letter-spacing="-4">12:30</text>
<rect x="64" y="332" width="220" height="16" rx="8" fill="{m3onSurface}" fill-opacity="0.75"/>
<rect x="56" y="548" width="520" height="196" rx="32" fill="{m3surfaceContainerLow}"/>
<rect x="76" y="568" width="156" height="156" rx="22" fill="url(#art)"/>
<rect x="256" y="592" width="200" height="18" rx="9" fill="{m3onSurface}"/>
<rect x="256" y="622" width="130" height="14" rx="7" fill="{m3onSurfaceVariant}"/>
<rect x="256" y="660" width="290" height="8" rx="4" fill="{m3secondaryContainer}"/>
<rect x="256" y="660" width="170" height="8" rx="4" fill="{m3primary}"/>
<rect x="256" y="686" width="76" height="40" rx="20" fill="{m3primary}"/>
<path d="M287 696 L287 716 L304 706 Z" fill="{m3onPrimary}"/>
<circle cx="364" cy="706" r="18" fill="{m3secondaryContainer}"/>
<circle cx="408" cy="706" r="18" fill="{m3secondaryContainer}"/>
<rect x="864" y="92" width="392" height="688" rx="32" fill="{m3background}"/>
<circle cx="910" cy="140" r="22" fill="{m3primaryContainer}"/>
<rect x="946" y="128" width="120" height="14" rx="7" fill="{m3onSurface}"/>
<rect x="946" y="150" width="80" height="10" rx="5" fill="{m3onSurfaceVariant}"/>
<circle cx="1212" cy="140" r="18" fill="{m3surfaceContainerHigh}"/>
{toggles}
<rect x="888" y="420" width="344" height="44" rx="22" fill="{m3secondaryContainer}"/>
<rect x="888" y="420" width="220" height="44" rx="22" fill="{m3primary}"/>
<circle cx="914" cy="442" r="9" fill="{m3onPrimary}"/>
<rect x="888" y="484" width="344" height="124" rx="24" fill="{m3surfaceContainerHigh}"/>
<circle cx="926" cy="522" r="18" fill="{m3tertiaryContainer}"/>
<circle cx="926" cy="522" r="7" fill="{m3tertiary}"/>
<rect x="956" y="510" width="140" height="13" rx="6.5" fill="{m3onSurface}"/>
<rect x="1180" y="512" width="32" height="9" rx="4.5" fill="{m3onSurfaceVariant}"/>
<rect x="956" y="534" width="250" height="9" rx="4.5" fill="{m3onSurfaceVariant}" fill-opacity="0.75"/>
<rect x="956" y="552" width="190" height="9" rx="4.5" fill="{m3onSurfaceVariant}" fill-opacity="0.75"/>
<rect x="956" y="574" width="84" height="20" rx="10" fill="{m3secondaryContainer}"/>
<rect x="1048" y="574" width="84" height="20" rx="10" fill="{m3secondaryContainer}"/>
<rect x="888" y="628" width="344" height="132" rx="24" fill="{m3surfaceContainerLow}"/>
{days}
<title>{name}</title>
</svg>
`

// hsl is #rrggbb from HSL in degrees and 0..1.
func hsl(hue, sat, light float64) string {
	c := (1 - math.Abs(2*light-1)) * sat
	x := c * (1 - math.Abs(math.Mod(hue/60, 2)-1))
	m := light - c/2
	rgb := [6][3]float64{{c, x, 0}, {x, c, 0}, {0, c, x}, {0, x, c}, {x, 0, c}, {c, 0, x}}[int(math.Mod(hue, 360)/60)]
	out := "#"
	for _, v := range rgb {
		out += fmt.Sprintf("%02x", int(math.RoundToEven((v+m)*255)))
	}
	return out
}

// iRiS's named accents, as hues, for the card drawn when an iRiS theme ships no screenshot.
var irisHues = map[string]float64{"red": 4, "orange": 28, "yellow": 48, "green": 142, "teal": 176, "sky": 200,
	"blue": 214, "indigo": 240, "purple": 272, "pink": 330}

func number(v any, fallback float64) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case string:
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return f
		}
	}
	return fallback
}

// irisCard is a card for an iRiS theme that ships no screenshot: its accent, highlight and shape on the Island.
func irisCard(e *entry) string {
	values := e.values
	hueOf := func(choiceKey, hueKey string, fallback float64, has bool) (float64, bool) {
		choice := fmt.Sprint(values[choiceKey])
		if choice == "custom" {
			return number(values[hueKey], fallback), true
		}
		if h, ok := irisHues[choice]; ok {
			return h, true
		}
		return fallback, has
	}
	accentHue, hasAccent := hueOf("iris.appearance.accent", "iris.appearance.theme.accentHue", 0, false)
	highlightHue, _ := hueOf("iris.appearance.highlight", "iris.appearance.theme.highlightHue", 32, true)
	accent := "#a7adb4"
	hue := 220.0
	if hasAccent {
		accent, hue = hsl(accentHue, 0.52, 0.58), accentHue
	}
	highlight := hsl(highlightHue, 0.82, 0.62)
	shape := math.Max(0, math.Min(2, number(values["iris.appearance.theme.shape"], 100)/100))
	round := func(v float64) int { return int(math.RoundToEven(v * shape)) }
	body, plate, ink := hsl(hue, 0.10, 0.10), hsl(hue, 0.10, 0.17), hsl(hue, 0.12, 0.88)
	var apps []string
	for i := 0; i < 5; i++ {
		fill := fmt.Sprintf(`fill="%s" fill-opacity="0.16"/>`, ink)
		if i == 2 {
			fill = fmt.Sprintf(`fill="%s"/>`, accent)
		}
		apps = append(apps, fmt.Sprintf(`<rect x="%d" y="682" width="56" height="56" rx="%d" %s`, 476+i*68, round(14), fill))
	}
	return strings.NewReplacer(
		"{top}", hsl(hue, 0.22, 0.16), "{bottom}", hsl(hue, 0.30, 0.07), "{body}", body, "{plate}", plate,
		"{ink}", ink, "{accent}", accent, "{highlight}", highlight, "{island_r}", fmt.Sprint(round(30)),
		"{plate_r}", fmt.Sprint(round(22)), "{dock_r}", fmt.Sprint(round(26)), "{apps}", strings.Join(apps, "\n"),
		"{name}", html.EscapeString(text(e.item, "name")),
	).Replace(irisSVG)
}

const irisSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="1280" height="800" viewBox="0 0 1280 800">
<defs><linearGradient id="sky" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="{top}"/><stop offset="1" stop-color="{bottom}"/></linearGradient></defs>
<rect width="1280" height="800" fill="url(#sky)"/>
<rect x="380" y="0" width="520" height="560" rx="{island_r}" fill="{body}" fill-opacity="0.92"/>
<rect x="580" y="32" width="120" height="16" rx="8" fill="{ink}" fill-opacity="0.85"/>
<rect x="420" y="96" width="440" height="200" rx="{plate_r}" fill="{plate}"/>
<circle cx="520" cy="196" r="64" fill="none" stroke="{ink}" stroke-opacity="0.12" stroke-width="12"/>
<circle cx="520" cy="196" r="64" fill="none" stroke="{accent}" stroke-width="12" stroke-linecap="round"
        stroke-dasharray="282 402" transform="rotate(-90 520 196)"/>
<rect x="624" y="160" width="180" height="22" rx="11" fill="{ink}" fill-opacity="0.85"/>
<rect x="624" y="200" width="132" height="16" rx="8" fill="{ink}" fill-opacity="0.45"/>
<circle cx="816" cy="140" r="10" fill="{highlight}"/>
<rect x="420" y="320" width="440" height="88" rx="{plate_r}" fill="{plate}"/>
<rect x="452" y="358" width="376" height="12" rx="6" fill="{ink}" fill-opacity="0.12"/>
<rect x="452" y="358" width="236" height="12" rx="6" fill="{accent}"/>
<rect x="420" y="432" width="208" height="96" rx="{plate_r}" fill="{accent}"/>
<rect x="652" y="432" width="208" height="96" rx="{plate_r}" fill="{plate}"/>
<circle cx="700" cy="480" r="18" fill="{highlight}"/>
<rect x="452" y="668" width="376" height="84" rx="{dock_r}" fill="{body}" fill-opacity="0.92"/>
{apps}
<title>{name}</title>
</svg>
`
