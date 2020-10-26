# Dashborg Go SDK

Dashborg was built to make connecting your code to the web as simple as writing to the console, reading/writing files, and reading command line arguments.  http://dashborg.net

## Documentation

* GoDoc (godoc.org): https://godoc.org/github.com/sawka/dashborg-go-sdk/pkg/dash
* GoDoc (pkg.go.dev): https://pkg.go.dev/github.com/sawka/dashborg-go-sdk/pkg/dash
* Docs: https://docs.dashborg.net/docs/getting-started/

## Getting Started

To get started,, you'll need three things:

* Go development environment
* Chrome web browser
* Dashborg SDK

To get started with Go, you'll need a working Go development environment.  If you haven't set up go before, here's the Go's official download/install page: https://golang.org/doc/install.  If you're using OS X, I recommend homebrew:

```
brew install go
```

Currently Dashborg only supports the Chrome web browser.  If you don't have it installed, you can install it here: https://www.google.com/chrome/.

The Dashborg SDK is availble on github at https://github.com/sawka/dashborg-go-sdk.

```
go get github.com/sawka/dashborg-go-sdk
```

## Dashborg Hello World

The code below is the complete code for your first Dashborg program.  Copy and paste it into your a new go file "demo.go".

```Go
package main

import (
    "github.com/sawka/dashborg-go-sdk/pkg/dash"
)

func main() {
    cfg := &dash.Config{ProcName: "hello", AnonAcc: true, AutoKeygen: true}
    defer dash.StartProcClient(cfg).WaitForClear()
    panel := dash.DefinePanel("default")
    panel.Print("[@h1] Hello World")
    panel.Flush()
}
```

You run the program like any other go program:

```
go run demo.go
```

You should see output that looks similar to:

<pre><code>&gt; go run demo.go
1| Dashborg Created new self-signed keypair key:dashborg-client.key cert:dashborg-client.crt for new accountid:<span style="color: red">[YOUR NEW ACCOUNT ID]</span>
2| Dashborg Initialized Client Zone:default ProcName:demo1 ProcRunId:fd542cff-5a7f-4e86-ab37-26dd9ae54154
3| Dashborg Zone [default] Link <span style="font-weight: bold">https://console.dashborg.net/acc/[YOUR NEW ACCOUNT ID]/default</span>
4| Dashborg Client Connected to proc.api.dashborg.net:7533
...
</code></pre>

Line #1: The first time you run your program, if you've set "AutoKeygen" to true, the Dashborg SDK will create a new Account Id for you and a self signed keypair in your current directory.  The default filenames for the keypair are "dashborg-client.key" (private key) and dashborg-client.crt (public certificate). You can learn more about how Dashborg is configured and uses these keys in the advanced configuration section of these docs.

Line #3: Here is the link to your new dashboard!  Copy and paste the link into your (Chrome) browser and you should see your newly created dashboard that says "Hello World".

That's it, you've created a new unregistered Dashborg account, and defined your first Dashborg panel.


## Dashborg Demos

There are two demo programs in this repository.  You can run them from the root module directory (demo2 references a file by relative path of "cmd/demo2/demo2-panel.txt").  They will create a demo1 and demo2 panel in your account.  To navigate to the demo pages, you can follow the panel links output by the demo programs.

```
go run cmd/demo1/demo1_main.go
go run cmd/demo2/demo2_main.go
```

Demo1 shows how to setup a simple panel with a button and log.  The button triggers some backend code and drives a progess control.

Demo2 shows how to setup a simple webapp that manages a set of fake customer accounts.  It uses handlers, requests, and ephemeral contexts.  When you enter the demo2 panel, click on "refresh" to show the initial account list.
