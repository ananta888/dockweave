package mounts

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// MountInfo is one parsed entry of /proc/self/mountinfo.
type MountInfo struct {
	ID         int
	ParentID   int
	MajorMinor string
	Root       string
	MountPoint string
	Options    string
	Optional   []string // e.g. "shared:3", "master:7"
	FSType     string
	Source     string
	SuperOpts  string
}

// Shared reports whether the mount belongs to a shared peer group.
func (m MountInfo) Shared() bool {
	for _, o := range m.Optional {
		if strings.HasPrefix(o, "shared:") {
			return true
		}
	}
	return false
}

// MasterPeer returns the peer-group id if this mount is a slave, else 0.
func (m MountInfo) MasterPeer() int {
	for _, o := range m.Optional {
		if strings.HasPrefix(o, "master:") {
			n, _ := strconv.Atoi(strings.TrimPrefix(o, "master:"))
			return n
		}
	}
	return 0
}

// Readonly reports whether the mount has the ro flag.
func (m MountInfo) Readonly() bool {
	for _, o := range strings.Split(m.Options, ",") {
		if o == "ro" {
			return true
		}
	}
	return false
}

// ReadMountinfo parses /proc/self/mountinfo (or a custom path for tests).
func ReadMountinfo(path string) ([]MountInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open mountinfo: %w", err)
	}
	defer f.Close()
	var out []MountInfo
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		mi, err := parseMountinfoLine(line)
		if err != nil {
			return nil, err
		}
		out = append(out, mi)
	}
	return out, sc.Err()
}

func parseMountinfoLine(line string) (MountInfo, error) {
	var mi MountInfo
	// Format: id parent major:minor root mountpoint options optional... - fstype source superopts
	sep := strings.Index(line, " - ")
	if sep < 0 {
		return mi, fmt.Errorf("malformed mountinfo line (no separator): %q", line)
	}
	left, right := line[:sep], line[sep+3:]
	lf := strings.Fields(left)
	rf := strings.Fields(right)
	if len(lf) < 6 || len(rf) < 3 {
		return mi, fmt.Errorf("malformed mountinfo line: %q", line)
	}
	mi.ID, _ = strconv.Atoi(lf[0])
	mi.ParentID, _ = strconv.Atoi(lf[1])
	mi.MajorMinor = lf[2]
	mi.Root = unescape(lf[3])
	mi.MountPoint = unescape(lf[4])
	mi.Options = lf[5]
	if len(lf) > 6 {
		mi.Optional = lf[6:]
	}
	mi.FSType = rf[0]
	mi.Source = unescape(rf[1])
	mi.SuperOpts = strings.Join(rf[2:], " ")
	return mi, nil
}

// unescape decodes octal escapes used by the kernel for space/tab/newline/backslash.
func unescape(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && isOct(s[i+1]) && isOct(s[i+2]) && isOct(s[i+3]) {
			b.WriteByte(byte((s[i+1]-'0')*64 + (s[i+2]-'0')*8 + (s[i+3] - '0')))
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOct(c byte) bool { return c >= '0' && c <= '7' }

// MountsUnder returns all mountinfo entries whose mountpoint is exactly
// prefix or a descendant of prefix.
func MountsUnder(infos []MountInfo, prefix string) []MountInfo {
	var out []MountInfo
	for _, mi := range infos {
		if mi.MountPoint == prefix || strings.HasPrefix(mi.MountPoint, strings.TrimSuffix(prefix, "/")+"/") {
			out = append(out, mi)
		}
	}
	return out
}
