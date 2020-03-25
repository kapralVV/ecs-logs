package lib

import (
	"github.com/apex/log"
	"github.com/kapralVV/ecs-logs-go"
	"github.com/kapralVV/ecs-logs-go/apex"
)

type LogLevel log.Level

func (lvl *LogLevel) Set(s string) error {
	if l, e := log.ParseLevel(s); e != nil {
		return e
	} else {
		*lvl = LogLevel(l)
		return nil
	}
}

func (lvl LogLevel) Get() interface{} {
	return lvl
}

func (lvl LogLevel) String() string {
	return log.Level(lvl).String()
}

type LogHandler struct {
	Group    string
	Stream   string
	Hostname string
	Queue    *MessageQueue
}

func (h *LogHandler) HandleLog(entry *log.Entry) (err error) {
	msg := Message{
		Group:  h.Group,
		Stream: h.Stream,
		Event:  apex_ecslogs.MakeEvent(entry),
	}

	if len(msg.Event.Info.Host) == 0 {
		msg.Event.Info.Host = h.Hostname
	}

	if len(msg.Event.Info.Source) == 0 {
		if pc, ok := ecslogs.GuessCaller(0, 10, "github.com/kapralVV/ecs-logs/lib", "github.com/apex/log"); ok {
			if info, ok := ecslogs.GetFuncInfo(pc); ok {
				msg.Event.Info.Source = info.String()
			}
		}
	}

	h.Queue.Push(msg)
	h.Queue.Notify()
	return
}
