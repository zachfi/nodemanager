package files

import (
	"fmt"
	"os/exec"
	"os/user"
	"strings"
)

// lookupUID resolves a username to a numeric UID using getent(1), which
// queries NSS and therefore works with LDAP, SSSD, and other remote
// user databases — unlike os/user.Lookup() which falls back to reading
// /etc/passwd directly when CGO_ENABLED=0.
//
// Falls back to os/user.Lookup() if getent is unavailable or fails.
func lookupUID(username string) (string, error) {
	out, err := exec.Command("getent", "passwd", username).Output()
	if err == nil {
		// Format: name:password:uid:gid:gecos:home:shell
		fields := strings.SplitN(strings.TrimSpace(string(out)), ":", 4)
		if len(fields) >= 3 {
			return fields[2], nil
		}
	}

	u, err := user.Lookup(username)
	if err != nil {
		return "", fmt.Errorf("lookup user %s: %w", username, err)
	}
	return u.Uid, nil
}

// lookupGID resolves a group name to a numeric GID using getent(1).
// Falls back to os/user.LookupGroup() if getent is unavailable or fails.
func lookupGID(group string) (string, error) {
	out, err := exec.Command("getent", "group", group).Output()
	if err == nil {
		// Format: name:password:gid:members
		fields := strings.SplitN(strings.TrimSpace(string(out)), ":", 4)
		if len(fields) >= 3 {
			return fields[2], nil
		}
	}

	g, err := user.LookupGroup(group)
	if err != nil {
		return "", fmt.Errorf("lookup group %s: %w", group, err)
	}
	return g.Gid, nil
}

// lookupUsernameByUID resolves a numeric UID to a username using getent(1).
// Falls back to os/user.LookupId(). Returns empty string on failure (best-effort).
func lookupUsernameByUID(uid string) string {
	out, err := exec.Command("getent", "passwd", uid).Output()
	if err == nil {
		fields := strings.SplitN(strings.TrimSpace(string(out)), ":", 2)
		if len(fields) >= 1 && fields[0] != "" {
			return fields[0]
		}
	}

	if u, err := user.LookupId(uid); err == nil {
		return u.Username
	}
	return ""
}

// lookupGroupnameByGID resolves a numeric GID to a group name using getent(1).
// Falls back to os/user.LookupGroupId(). Returns empty string on failure (best-effort).
func lookupGroupnameByGID(gid string) string {
	out, err := exec.Command("getent", "group", gid).Output()
	if err == nil {
		fields := strings.SplitN(strings.TrimSpace(string(out)), ":", 2)
		if len(fields) >= 1 && fields[0] != "" {
			return fields[0]
		}
	}

	if g, err := user.LookupGroupId(gid); err == nil {
		return g.Name
	}
	return ""
}
