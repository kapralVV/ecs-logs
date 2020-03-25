package logdna

import "github.com/kapralVV/ecs-logs/lib"

func init() {
	lib.RegisterDestination("logdna", lib.DestinationFunc(NewWriter))
}
