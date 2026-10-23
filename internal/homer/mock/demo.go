package mock

import "fmt"

// DemoYAML is a homer-tui config for the emulator at url: a host that logs
// in with a password, one with an API token (settings changes are refused
// then, as by homer-app), one with a wrong password and one that does not
// answer, so that the host list shows each case.
func DemoYAML(url, token string) string {
	return fmt.Sprintf(`# homer-tui config for the emulator (homer-tui-mock -confdir)
currentHost: lab
defaultSince: 4h
hosts:
  - {name: lab, url: %[1]q, username: admin, password: sipcapture, labels: {role: lab}}
  - {name: lab-token, url: %[1]q, token: %[2]q, labels: {role: lab, case: token-read-only}}
  - {name: wrong-password, url: %[1]q, username: admin, password: wrong, labels: {case: login-fails}}
  - {name: down, url: "http://127.0.0.1:1", username: admin, password: sipcapture, labels: {case: unreachable}}
`, url, token)
}
