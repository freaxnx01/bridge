package bot

import (
	"strconv"
	"strings"
)

// data builds callback data "<epoch>:<verb>[:<arg>…]". Buttons only ever
// carry short ids resolved against the chat's State — never paths or names.
func data(epoch int, verb string, args ...string) string {
	return strings.Join(append([]string{strconv.Itoa(epoch), verb}, args...), ":")
}

// tapData is parsed callback data.
type tapData struct {
	epoch int
	verb  string
	args  []string
}

func parseData(d string) (tapData, bool) {
	if d == "" || len(d) > MaxCallbackBytes {
		return tapData{}, false
	}
	parts := strings.Split(d, ":")
	if len(parts) < 2 {
		return tapData{}, false
	}
	epoch, err := strconv.Atoi(parts[0])
	if err != nil {
		return tapData{}, false
	}
	return tapData{epoch: epoch, verb: parts[1], args: parts[2:]}, true
}

// intArg parses args[i] ("+1" and "-1" included).
func (t tapData) intArg(i int) (int, bool) {
	if i >= len(t.args) {
		return 0, false
	}
	n, err := strconv.Atoi(t.args[i])
	return n, err == nil
}
