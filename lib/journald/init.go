// +build linux

package journald

import "github.com/kapralVV/ecs-logs/lib"

func init() {
	lib.RegisterSource("journald", lib.SourceFunc(NewReader))
}
