//go:build js && wasm

// A browser entry point for the demo at grace.github.io/demos/warden.
//
// The point of compiling rather than reimplementing: the page runs this exact
// projection, so a demo cannot quietly diverge from the tool it advertises.
package main

import (
	"encoding/json"
	"syscall/js"
)

// project(contractJSON, traceJSON, audience) -> {ok, decision, error}
//
// Errors are returned as values rather than thrown: a refusal to publish is a
// normal outcome here, and the page needs to render it as one.
func project(_ js.Value, args []js.Value) any {
	if len(args) != 3 {
		return result(nil, "project(contract, trace, audience) takes three arguments")
	}

	var c Contract
	if err := json.Unmarshal([]byte(args[0].String()), &c); err != nil {
		return result(nil, "contract: "+err.Error())
	}
	if err := c.validate(); err != nil {
		return result(nil, "contract: "+err.Error())
	}

	var t Trace
	if err := json.Unmarshal([]byte(args[1].String()), &t); err != nil {
		return result(nil, "trace: "+err.Error())
	}

	decision, err := Project(&t, &c, Audience(args[2].String()))
	if err != nil {
		return result(nil, err.Error())
	}

	out, err := json.MarshalIndent(decision, "", "  ")
	if err != nil {
		return result(nil, err.Error())
	}
	return result(out, "")
}

func result(decision []byte, errMsg string) map[string]any {
	if errMsg != "" {
		return map[string]any{"ok": false, "error": errMsg}
	}
	return map[string]any{"ok": true, "decision": string(decision)}
}

func lint(_ js.Value, args []js.Value) any {
	if len(args) != 1 {
		return result(nil, "lint(contract) takes one argument")
	}
	var c Contract
	if err := json.Unmarshal([]byte(args[0].String()), &c); err != nil {
		return result(nil, "contract: "+err.Error())
	}
	findings := Lint(&c)
	out, err := json.MarshalIndent(findings, "", "  ")
	if err != nil {
		return result(nil, err.Error())
	}
	return map[string]any{"ok": true, "findings": string(out)}
}

func main() {
	js.Global().Set("wardenProject", js.FuncOf(project))
	js.Global().Set("wardenLint", js.FuncOf(lint))
	js.Global().Get("dispatchEvent").Invoke(
		js.Global().Get("Event").New("warden-ready"))
	select {} // keep the exported funcs alive
}
