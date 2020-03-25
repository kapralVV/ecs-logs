package syslog

import "github.com/kapralVV/ecs-logs/lib"

func init() {
	lib.RegisterDestination("syslog", lib.DestinationFunc(NewWriter))
}
