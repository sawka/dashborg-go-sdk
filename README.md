# Dashborg Go SDK

Dashborg was built to make connecting your code to the web as simple as writing to the console, reading/writing files, and reading command line arguments.  http://dashborg.net

Live link to SDK demos:
https://console.dashborg.net/acc/421d595f-9e30-4178-bcc3-b853f890fb8e/default/demo1

## Documentation

The public API for dashborg is in the **github.com/sawka/dashborg-go-sdk/pkg/dash** package.  Utility functions are provided in **github.com/sawka/dashborg-go-sdk/pkg/dashutil**.  The other packages are internal and not intended to be directly imported into client code.

* GoDoc (godoc.org): https://godoc.org/github.com/sawka/dashborg-go-sdk/pkg/dash
* GoDoc (pkg.go.dev): https://pkg.go.dev/github.com/sawka/dashborg-go-sdk/pkg/dash
* Docs: https://docs.dashborg.net/docs/getting-started/

Questions? [Join the Dashborg Slack!](https://join.slack.com/t/dashborgworkspace/shared_invite/zt-iey7ebif-Nps2uXQivdFFlPz63rDb2w)

## Getting Started

To get started,, you'll need three things:

* Go development environment
* Chrome web browser
* Dashborg SDK

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
2| Dashborg KeyFile:[KEYFILE-NAME] CertFile:[CERTFILE-NAME] SHA256:[YOUR PUBLIC KEY SHA256 HASH]
3| Dashborg Zone [default] Link https://console.dashborg.net/acc/[ACCOUNT-ID]/default
4| Dashborg Client Connected to proc.api.dashborg.net:7533
5| Dashborg Defined Panel [default] Link https://console.dashborg.net/acc/[ACCOUNT-ID]/default/default
...
</code></pre>

Line #1: The first time you run your program, if you've set "AutoKeygen" to true, the Dashborg SDK will create a new Account Id for you and a self signed keypair in your current directory.  The default filenames for the keypair are "dashborg-client.key" (private key) and dashborg-client.crt (public certificate). This line is only displayed the first time you run the program (when the keys are generated).

Line #2: This line shows the keys being used and the SHA256 fingerprint of your Dashborg Public Key.  You'll need this value to claim/administer your account should you choose to register on the Dashborg site.

Line #5: Here's the link to your new panel.  Paste it into Chrome and you should see your "Hello World" panel!

That's it, you've created a new unregistered Dashborg account, and defined your first Dashborg panel.


## Dashborg Demos

Demos and tutorials are contained in the "cmd" directories.  All can be run from the root SDK directory:

```
go run cmd/demo1/demo1_main.go
go run cmd/demo2/demo2_main.go
go run cmd/tutorial-container/container_main.go
go run cmd/tutorial-text/text_main.go
```

The text tutorial shows how to format text and containers including font size, color, background colors, text effects, margin, padding, etc.  The container tutorial shows some container basics, including how to align rows/columns and position dashborg elements.  Feel free to edit the tutorials and re-run them.  When you run them again your panel will automatically change to reflect the new code.

Demo1 shows how to setup a simple panel with a button and log.  The button triggers some backend code and drives a progess control.

Demo2 shows how to setup a simple webapp that manages a set of fake customer accounts.  It uses handlers, requests, and ephemeral contexts.  When you enter the demo2 panel, click on "refresh" to show the initial account list.

## More

Feel free to contact me with any questions on Slack (see invite link above), on github (sawka), or by email: mike (at) dashborg.net.  This project is under active development and the API will change as I add new controls/functionality.
