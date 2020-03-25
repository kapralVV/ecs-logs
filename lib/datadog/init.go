package datadog

import "github.com/kapralVV/ecs-logs/lib"

func init() {
	lib.RegisterDestination("datadog", lib.DestinationFunc(NewWriter))
}
