// +build linux

package journald

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coreos/go-systemd/sdjournal"
	"github.com/kapralVV/ecs-logs-go"
	"github.com/kapralVV/ecs-logs/lib"
)

func NewReader() (r lib.Reader, err error) {
	var j *sdjournal.Journal

	if j, err = sdjournal.NewJournal(); err != nil {
		return
	}

	if err = j.SeekTail(); err != nil {
		j.Close()
		return
	}

	var streamName string
	if streamName = os.Getenv("JOURNALD_STREAM_NAME"); len(streamName) == 0 {
		streamName = "CONTAINER_ID_FULL"
	}

	r = &reader{Journal: j, streamName: streamName}
	return
}

type reader struct {
	streamName string
	stopped    int32
	*sdjournal.Journal
}

func (r *reader) Close() (err error) {
	atomic.StoreInt32(&r.stopped, 1)
	return
}

func (r *reader) ReadMessage() (msg lib.Message, err error) {
	for atomic.LoadInt32(&r.stopped) == 0 {
		var cur int
		var ok bool

		if cur, err = r.Next(); err != nil {
			return
		}

		if cur == 0 {
			r.Wait(1 * time.Second)
			continue
		}

		if msg, ok, err = r.getMessage(); ok || err != nil {
			return
		}
	}

	r.Journal.Close()
	err = io.EOF
	return
}

func stringToRawMessage(str string) (json.RawMessage, bool) {
    var js json.RawMessage
	err := json.Unmarshal([]byte(str), &js)
	return js, (err == nil)
}

func (r *reader) getMessage() (msg lib.Message, ok bool, err error) {
	if msg.Group, err = r.GetDataValue("CONTAINER_TAG"); len(msg.Group) == 0 {
		// No CONTAINER_TAG, this must be a journal message from a process that
		// isn't running in a docker container.
		err = nil
		return
	}

	if msg.Stream, err = r.GetDataValue(r.streamName); err != nil {
		// Fallback to CONTAINER_ID_FULL
		if msg.Stream, err = r.GetDataValue("CONTAINER_ID_FULL"); err != nil {
			// There's a CONTAINER_TAG but no CONTAINER_ID_FULL, something is seriously
			// wrong here, the log docker log driver is misbehaving.
			err = fmt.Errorf("missing CONTAINER_ID_FULL in message with CONTAINER_TAG=%s", msg.Group)

			return
		}
	}

	msg.Stream = sanitizeStreamName(msg.Stream)

	s := r.getString("MESSAGE")
	if raw, ok := stringToRawMessage(s); ok {
		if unquoted, err :=  strconv.Unquote(s); err == nil {
			if raw1, ok1 := stringToRawMessage(unquoted); ok1 {
				msg.Event.Message = raw1
				msg.Event.IsMessageJson = true
				msg.Event.WasMessagequoted = true
			} else {
				msg.Event.Message = raw
				msg.Event.IsMessageJson = true
				msg.Event.WasMessagequoted = false
			}
		} else {
			msg.Event.Message = raw
				msg.Event.IsMessageJson = true
			msg.Event.WasMessagequoted = false
		}
	} else {
		string_raw, _ := json.Marshal(s)
		msg.Event.Message = json.RawMessage(string(string_raw))
		msg.Event.IsMessageJson = false
		msg.Event.WasMessagequoted = false
	}

	if msg.Event.Level == ecslogs.NONE {
		msg.Event.Level = r.getPriority()
	}

	if len(msg.Event.Info.Host) == 0 {
		msg.Event.Info.Host = r.getString("_HOSTNAME")
	}

	if len(msg.Event.Info.Source) == 0 {
		msg.Event.Info.Source = (ecslogs.FuncInfo{
			File: r.getString("CODE_FILE"),
			Func: r.getString("CODE_FUNC"),
			Line: r.getInt("CODE_LINE"),
		}).String()
	}

	if len(msg.Event.Info.ID) == 0 {
		msg.Event.Info.ID = r.getString("MESSAGE_ID")
	}

	if msg.Event.Info.PID == 0 {
		msg.Event.Info.PID = r.getInt("_PID")
	}

	if msg.Event.Info.GID == 0 {
		msg.Event.Info.GID = r.getInt("_GID")
	}

	if msg.Event.Info.UID == 0 {
		msg.Event.Info.UID = r.getInt("_UID")
	}

	if msg.Event.Time == (time.Time{}) {
		msg.Event.Time = r.getTime()
	}

	ok = true
	return
}

func (r *reader) getInt(k string) (v int) {
	v, _ = strconv.Atoi(r.getString(k))
	return
}

func (r *reader) getTime() (t time.Time) {
	if u, e := r.GetRealtimeUsec(); e == nil {
		t = time.Unix(int64(u/1000000), int64((u%1000000)*1000))
	} else {
		t = time.Now()
	}
	return
}

func (r *reader) getPriority() (p ecslogs.Level) {
	if v, e := strconv.Atoi(r.getString("PRIORITY")); e != nil {
		p = ecslogs.INFO
	} else {
		p = ecslogs.MakeLevel(v)
	}
	return
}

func (r *reader) getString(k string) (s string) {
	s, _ = r.GetDataValue(k)
	return
}

func sanitizeStreamName(name string) string {
	name = strings.Replace(name, ":", "/", -1)
	name = strings.Replace(name, "*", "/", -1)
	max := len(name)
	if max > 512 {
		max = 512
	}
	return name[:max]
}
