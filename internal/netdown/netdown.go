// Package netdown names a server that is down as the cause of a failed download. People read the red line that
// follows a GitHub outage (its 504s, 11-09) as iNiR broken; setup says the same through net_down
// (sdata/lib/environment-variables.sh).
package netdown

import (
	"fmt"
	"net/http"
	"strings"
)

// Cause is the sentence for a response whose server says it is down (5xx, 429), or "" for any other.
func Cause(resp *http.Response) string {
	if resp == nil || !(resp.StatusCode >= 500 && resp.StatusCode <= 599 || resp.StatusCode == http.StatusTooManyRequests) {
		return ""
	}
	host := ""
	if resp.Request != nil && resp.Request.URL != nil {
		host = resp.Request.URL.Hostname()
	}
	if github(host) || strings.Contains(strings.ToLower(resp.Header.Get("Server")), "github") {
		return fmt.Sprintf("GitHub is having trouble right now (HTTP %d from %s). That's GitHub, not iNiR or your system: "+
			"try again in a while (https://www.githubstatus.com)", resp.StatusCode, host)
	}
	return fmt.Sprintf("%s is having trouble right now (HTTP %d). That's that server, not iNiR or your system: "+
		"try again in a while", host, resp.StatusCode)
}

func github(host string) bool {
	for _, d := range []string{"github.com", "githubusercontent.com", "github.io"} {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}
