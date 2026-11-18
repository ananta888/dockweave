// dockweave-mountd is the small privileged helper for DockWeave mounts.
// It accepts directory file descriptors from dockweaved and performs only
// the mount syscall, with targets restricted to the DockWeave state root.
package main

import (
	"flag"
	"log"
	"os"
	"runtime"

	"github.com/ananta888/dockweave/internal/mounthelper"
)

type uids []uint32

func (u *uids) String() string { return "" }

func (u *uids) Set(v string) error {
	var n uint32
	for _, c := range v {
		if c < '0' || c > '9' {
			return os.ErrInvalid
		}
		n = n*10 + uint32(c-'0')
	}
	*u = append(*u, n)
	return nil
}

func main() {
	socket := flag.String("socket", "/run/dockweave/mountd.sock", "helper socket")
	root := flag.String("root", "/var/lib/dockweave", "state root (mount targets must live below it)")
	var allowed uids
	flag.Var(&allowed, "uid", "allowed client uid (repeatable)")
	flag.Parse()

	if runtime.GOOS != "linux" {
		log.Fatalf("mountd requires Linux")
	}
	if os.Geteuid() != 0 {
		log.Fatalf("mountd must run as root (mount syscalls)")
	}
	if err := mounthelper.Serve(mounthelper.ServerConfig{
		Socket: *socket, StateRoot: *root, AllowedUIDs: allowed,
	}); err != nil {
		log.Fatalf("mountd: %v", err)
	}
}
