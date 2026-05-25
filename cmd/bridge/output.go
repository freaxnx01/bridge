package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func emitJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

func emitJSONError(msg string, code int) {
	type errOut struct {
		Error string `json:"error"`
		Code  int    `json:"code"`
	}
	b, _ := json.Marshal(errOut{Error: msg, Code: code})
	fmt.Fprintln(os.Stderr, string(b))
}
