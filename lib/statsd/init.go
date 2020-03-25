package statsd

import "github.com/kapralVV/ecs-logs/lib"

func init() {
	lib.RegisterDestination("statsd", lib.DestinationFunc(NewWriter))
}
