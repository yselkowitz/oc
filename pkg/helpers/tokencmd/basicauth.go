package tokencmd

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"k8s.io/klog/v2"

	"github.com/openshift/oc/pkg/helpers/term"
	"github.com/openshift/oc/pkg/version"
)

// BasicAuthNoUsernameError when basic authentication challenge handling was attempted
// but the required username was not provided from the command line options
type BasicAuthNoUsernameError struct{}

//
func (e *BasicAuthNoUsernameError) Error() string {
	return "did not receive username. Pass 'oc login --username <username>' for the password prompt"
}

// NewBasicAuthNoUsernameError returns an error for a basic challenge without a username
func NewBasicAuthNoUsernameError() error {
	return &BasicAuthNoUsernameError{}
}

type BasicChallengeHandler struct {
	// Host is the server being authenticated to. Used only for displaying messages when prompting for username/password
	Host string

	// serverVersionRetriever is used for fetching server version
	serverVersionRetriever version.ServerVersionRetriever

	// Reader is used to prompt for username/password. If nil, no prompting is done
	Reader io.Reader
	// Writer is used to output prompts. If nil, stdout is used
	Writer io.Writer

	// Username is the username to use when challenged. If empty, a prompt is issued to a non-nil Reader
	Username string
	// Password is the password to use when challenged. If empty, a prompt is issued to a non-nil Reader
	Password string

	// handled tracks whether this handler has already handled a challenge.
	handled bool
	// prompted tracks whether this handler has already prompted for a username and/or password.
	prompted bool
}

func (c *BasicChallengeHandler) CanHandle(headers http.Header) bool {
	isBasic, _ := basicRealm(headers)
	return isBasic
}
func (c *BasicChallengeHandler) HandleChallenge(requestURL string, headers http.Header) (http.Header, bool, error) {
	if c.prompted {
		klog.V(2).Info("already prompted for challenge, won't prompt again")
		return nil, false, nil
	}
	if c.handled {
		klog.V(2).Info("already handled basic challenge")
		return nil, false, nil
	}

	username := c.Username
	password := c.Password

	missingUsername := len(username) == 0
	missingPassword := len(password) == 0

	if missingUsername {
		return nil, false, NewBasicAuthNoUsernameError()
	}
	// Basic auth does not support usernames containing colons
	// http://tools.ietf.org/html/rfc2617#section-2
	if strings.Contains(username, ":") {
		return nil, false, fmt.Errorf("username %s is invalid for basic auth", username)
	}
	if missingPassword && c.Reader != nil {
		w := c.Writer
		if w == nil {
			w = os.Stdout
		}

		if _, realm := basicRealm(headers); len(realm) > 0 {
			fmt.Fprintf(w, "Authentication required for %s (%s)\n", c.Host, realm)
		} else {
			fmt.Fprintf(w, "Authentication required for %s\n", c.Host)
		}
		if c.serverVersionRetriever != nil {
			serverVersion, err := c.serverVersionRetriever.RetrieveServerVersion()
			// this feature was introduced in Openshift 4.11 which should correspond to 1.24
			if err == nil && serverVersion.MajorNumber >= 1 && serverVersion.MinorNumber >= 24 {
				fmt.Fprintf(w, "Console URL: %s/console\n", c.Host)
			}
		}
		fmt.Fprintf(w, "Username: %s\n", username)
		if missingPassword {
			password = term.PromptForPasswordString(c.Reader, w, "Password: ")
		}
		// remember so we don't re-prompt
		c.prompted = true
	}

	responseHeaders := http.Header{}
	responseHeaders.Set("Authorization", getBasicHeader(username, password))
	// remember so we don't re-handle non-interactively
	c.handled = true
	return responseHeaders, true, nil
}
func (c *BasicChallengeHandler) CompleteChallenge(requestURL string, headers http.Header) error {
	return nil
}

func (c *BasicChallengeHandler) Release() error {
	return nil
}

// if any of these match a WWW-Authenticate header, it is a basic challenge
// capturing group 1 (if present) should contain the realm
var basicRegexes = []*regexp.Regexp{
	// quoted realm
	regexp.MustCompile(`(?i)^\s*basic\s+realm\s*=\s*"(.*?)"\s*(,|$)`),
	// token realm
	regexp.MustCompile(`(?i)^\s*basic\s+realm\s*=\s*(.*?)\s*(,|$)`),
	// no realm
	regexp.MustCompile(`(?i)^\s*basic(?:\s+|$)`),
}

// basicRealm returns true if a header indicates a basic auth challenge,
// and the realm if one exists.
func basicRealm(headers http.Header) (bool, string) {
	for _, challengeHeader := range headers[http.CanonicalHeaderKey("WWW-Authenticate")] {
		for _, r := range basicRegexes {
			if matches := r.FindStringSubmatch(challengeHeader); matches != nil {
				if len(matches) > 1 {
					// We got a realm as well
					return true, matches[1]
				}
				// No realm, but still basic
				return true, ""
			}
		}
	}
	return false, ""
}
func getBasicHeader(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}
