package loggly

import "github.com/kapralVV/ecs-logs/lib"

func init() {
	lib.RegisterDestination("loggly", lib.DestinationFunc(NewWriter))
}
