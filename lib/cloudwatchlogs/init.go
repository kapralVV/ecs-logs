package cloudwatchlogs

import "github.com/kapralVV/ecs-logs/lib"

func init() {
	lib.RegisterDestination("cloudwatchlogs", newClient())
}
