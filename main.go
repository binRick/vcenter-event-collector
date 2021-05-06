/*
@author: Saad Zaher
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/gobwas/glob"
	"github.com/k0kubun/pp"
	"log"
	"os"
	"strings"

	"encoding/json"
	"net/url"
	"reflect"
	"time"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/event"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/types"
)

var (
	begin               time.Duration
	begin_dur           time.Duration
	begin_unit          string
	begin_qty           int
	end                 time.Duration
	follow              bool
	vcenterUrl          string
	specified_kind      string
	specified_msg_match string
	specified_mode      string
	specified_format    string
	insecure            bool
	username            string
	password            string
	eventCount          int
)

func init() {
	flag.IntVar(&begin_qty, "b", 10, "Begin Start Time quantity")
	flag.StringVar(&begin_unit, "U", `minute`, "Begin unit, m, d, h, etc")
	flag.DurationVar(&end, "e", 0, "End time of events to be streamed")
	flag.StringVar(&specified_kind, "k", `all`, "Limit events to this type")
	flag.StringVar(&specified_format, "o", `text`, "Output format- text, json")
	flag.StringVar(&specified_mode, "m", `list`, "Event Display Mode")
	flag.StringVar(&specified_msg_match, "M", `all`, "Event Message String Match. all, glob format and regex: *vmnic*, *error*, ^ERROR.*, etc.")
	flag.BoolVar(&follow, "f", false, "Follow event stream")
	flag.StringVar(&vcenterUrl, "url", "", "Vcenter URL. i.e. https://localhost/sdk")
	flag.StringVar(&username, "u", "administrator@vsphere.local", "Vcenter Username")
	flag.StringVar(&password, "p", "", "Vcenter password")
	flag.BoolVar(&insecure, "i", true, "Insecure")
	flag.IntVar(&eventCount, "c", 100, "Number of events to fetch every time.")

}

func main() {
	// example use against simulator: go run main.go -b 8h -f
	// example use against vCenter with optional event filters:
	// go run main.go -url $GOVMOMI_URL -insecure $GOVMOMI_INSECURE -b 8h -f VmEvent UserLoginSessionEvent
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	flag.Parse()
	var g glob.Glob
	switch begin_unit {
	case "m":
		begin = time.Duration(int32(begin_qty)) * time.Minute
	case "h":
		begin = time.Duration(int32(begin_qty)) * time.Hour * 1
	case "d":
		begin = time.Duration(int32(begin_qty)) * time.Hour * 24
	}

	if vcenterUrl == "" {
		fmt.Fprintf(os.Stderr, "-url vCenter url is Required\n")
		os.Exit(1)
	}
	if password == "" {
		fmt.Fprintf(os.Stderr, "-p Password is required\n")
		os.Exit(1)
	}

	u, _ := url.Parse(vcenterUrl)
	u.User = url.UserPassword(username, password)

	c, err := govmomi.NewClient(ctx, u, true)
	if err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(1)
	}
	c.Login(ctx, u.User)

	m := event.NewManager(c.Client)

	ref := c.ServiceContent.RootFolder

	now, err := methods.GetCurrentTime(ctx, c) // vCenter server time (UTC)
	if err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(1)
	}
	//	fmt.Printf("begin: %s\n", begin)

	filter := types.EventFilterSpec{
		EventTypeId: flag.Args(), // e.g. VmEvent
		Entity: &types.EventFilterSpecByEntity{
			Entity:    ref,
			Recursion: types.EventFilterSpecRecursionOptionAll,
		},
		Time: &types.EventFilterSpecByTime{
			BeginTime: types.NewTime(now.Add(begin * -1)),
		},
	}
	if end != 0 {
		filter.Time.EndTime = types.NewTime(now.Add(end * -1))
	}

	collector, err := m.CreateCollectorForEvents(ctx, filter)
	if err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(1)
	}

	defer collector.Destroy(ctx)
	if specified_msg_match != `all` {
		g = glob.MustCompile(specified_msg_match)
	}
	Events := Events{}
	for {
		events, err := collector.ReadNextEvents(ctx, int32(eventCount))
		if err != nil {
			fmt.Fprint(os.Stderr, err)
			os.Exit(1)
		}

		if len(events) == 0 {
			if follow {
				time.Sleep(time.Second)
				continue
			}
			break
		}
		for i := range events {
			event := events[i].GetEvent()
			kind := reflect.TypeOf(events[i]).Elem().Name()
			E := Event{
				Key:        event.Key,
				Created:    event.CreatedTime.Format(time.ANSIC),
				Kind:       kind,
				Show:       true,
				Message:    event.FullFormattedMessage,
				TextOutput: fmt.Sprintf("%d [%s] [%s] %s\n", event.Key, event.CreatedTime.Format(time.ANSIC), kind, event.FullFormattedMessage),
			}
			if specified_kind != `all` && E.Kind != specified_kind {
				E.Show = false
			}
			if E.Show && specified_msg_match != `all` && !g.Match(event.FullFormattedMessage) {
				E.Show = false
			}
			Events.add_kind(E.Kind)
			if E.Show {
				if specified_mode == `list` {
					fmt.Println(E.Output(specified_mode, specified_format))
				}
			}
			Events.Events = append(Events.Events, E)
			//			pp.Print(E)
		}
	}
	if Events.ShowSummary() {
		fmt.Println(Events.Output(specified_mode, specified_format))
	}
	if false {
		pp.Print(Events.Kinds)
	}
	//	pp.Print(Events)
	//	pp.Print(len(Events.Events))
}

type Events struct {
	Events []Event
	Kinds  []string
}

type Event struct {
	Key        int32
	Show       bool
	Created    string
	Kind       string
	Message    string
	TextOutput string
}

func (e *Event) Output(mode string, format string) string {
	o := `unknown mode`
	switch mode {
	case "list":
		switch format {
		case "json":
			return fmt.Sprintf("%s", e.JSON())
		case "text":
			return fmt.Sprintf("%d [%s] [%s] %s", e.Key, e.Created, e.Kind, e.Message)
		}
		return `unknown format`
	}

	return o
}

func (e *Event) JSON() string {
	j, err := json.Marshal(e)
	if err != nil {
		panic(err)
	}
	s := string(j)
	return s

}
func (E *Events) ShowSummary() bool {
	return (specified_mode == `kinds` || specified_mode == `summary`)
}

func (E *Events) Output(mode string, format string) string {
	o := `unknown mode`
	switch mode {
	case "summary":
		switch format {
		case "json":
			return fmt.Sprintf("json summamry\n")
		case "text":
			return fmt.Sprintf(`
# Events: %d
# Kinds:  %d

`,
				len(E.Events),
				len(E.Kinds),
			)
		}
		return `unknown format`
	case "kinds":
		switch format {
		case "json":
			return fmt.Sprintf("json\n")
		case "text":
			return strings.Join(E.Kinds, `, `)
		}
		return `unknown format`
	case "list":
		switch format {
		case "text":
			return strings.Join(E.Kinds, `, `)
		}
		return `unknown format`
	}
	return o
}
func (E *Events) add_kind(kind string) {
	has := false
	for _, k := range E.Kinds {
		if k == kind {
			has = true
			break
		}
	}
	if !has {
		E.Kinds = append(E.Kinds, kind)
	}
}

type Size int

const (
	Unrecognized Size = iota
	Small
	Large
)

func (s *Size) UnmarshalText(text []byte) error {
	switch strings.ToLower(string(text)) {
	default:
		*s = Unrecognized
	case "small":
		*s = Small
	case "large":
		*s = Large
	}
	return nil
}

func (s Size) MarshalText() ([]byte, error) {
	var name string
	switch s {
	default:
		name = "unrecognized"
	case Small:
		name = "small"
	case Large:
		name = "large"
	}
	return []byte(name), nil
}

func j() {
	blob := `["small","regular","large","unrecognized","small","normal","small","large"]`
	var inventory []Size
	if err := json.Unmarshal([]byte(blob), &inventory); err != nil {
		log.Fatal(err)
	}

	counts := make(map[Size]int)
	for _, size := range inventory {
		counts[size] += 1
	}

	fmt.Printf("Inventory Counts:\n* Small:        %d\n* Large:        %d\n* Unrecognized: %d\n",
		counts[Small], counts[Large], counts[Unrecognized])

}
