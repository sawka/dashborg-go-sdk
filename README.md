# Dashborg Go SDK

Dashborg was built to be the simplest way to create web-based internal tools directly from backend code.
Cut out the overhead of configuring and hosting a web stack, and stop wrestling with JavaScript, AJAX, and UI libraries.

## Documentation

* GoDoc (godoc.org): https://godoc.org/github.com/sawka/dashborg-go-sdk/pkg/dash
* GoDoc (pkg.go.dev): https://pkg.go.dev/github.com/sawka/dashborg-go-sdk/pkg/dash

## Key Features

* **No Javascript, No Context Switching** - Write your dashboards using pure HTML and backend code.  No JavaScript environment to set up, no messing with NPM, Yarn, Webpack, Babel, React, Angular, etc.
* **No Open Ports** - No webhooks, firewall configuration, IP whitelists, or server hosting required for your backend.
* **No Server Hosting** - You get a secure, internet accessible frontend out of the box.  No web server configuration, domain name, load balancer, WAF setup and configuration required.
* **No Shared Passwords** - No incoming connections to your infrastructure.  Dashborg does not need your database passwords or API keys.  It does not access any 3rd party service on your behalf.
* **Not No-Code** - Built for developers.  Dashborg cuts out the overhead and let's you use the code/libraries/frameworks that you already use to write your tools -- no 3rd-party GUI tools to learn, or typing code into text boxes on a website.  Easy to get started, but powerful enough to build complex tools and interactions.
* **Secure** - All connections are secured using SSL public/private key encryption with client auth.  HTTPS on the frontend.  Secure your dashboards with a simple password or user accounts.  SSO (coming soon).
* **Control** - Dashborg panels are 100% defined from your backend code.  That means you can version them in your own code repository, and run and test them in your current dev/staging/production environments.
* **Modern Frontend Controls** - Tables, Lists, Forms, Inputs, and Buttons all out of the box (with more to come).  No Javascript or CSS frameworks required.  All styled to look good and work together.

## Dashborg Hello World

The code below is the complete code for your first Dashborg panel.
Copy and paste it into a new go file "demo.go" and run it like any other
go program: ```go run demo.go```.  Click the Dashborg Panel Link to view
your live, deployed dashboard.

```
package main

import (
	"fmt"

	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

const PANEL_HTML = `
<panel>
  <h1>Demo Dashboard</h1>
  <d-button handler="/run-action">Run Action</d-button>
</panel>
`

func main() {
	cfg := &dash.Config{ProcName: "demo", AnonAcc: true, AutoKeygen: true}
	dash.StartProcClient(cfg)
	defer dash.WaitForClear()
	dash.RegisterPanelHandler("default", "/", func(req *dash.PanelRequest) error {
		req.NoAuth()
		req.SetHtml(PANEL_HTML)
		return nil
	})
	dash.RegisterPanelHandler("default", "/run-action", func(req *dash.PanelRequest) error {
		fmt.Printf("running backend action\n")
		return nil
	})
	select {}
}
```

When you run the program you'll see output similar to:

```
1| Dashborg created new self-signed keypair key:dashborg-client.key cert:dashborg-client.crt for new accountid:[YOUR NEW ACCOUNT ID]
2| Dashborg KeyFile:dashborg-client.key CertFile:dashborg-client.crt SHA256:[YOUR PUBLIC KEY SHA256 HASH]
3| Dashborg Initialized Client AccId:[ACCOUNT ID] Zone:default ProcName:demo
4| Dashborg Panel Link [default]: https://console.dashborg.net/acc/[ACCOUNT-ID]/default/default
```

Line #1/#2 - The first time you run your program a new public/private keypair will be generated in your current directory with a new random account id.

Line #4 - Here's the link to your new panel.  Copy/paste it into a browser or click on it to see your new dashboard.

That's it, you've created a new Dashborg account, and defined your first live Dashborg panel!

## Why Dashborg?

I started my career as a backend developer.  I loved how
straightforward backend code was.  You open files, read command line
arguments, and write to the console.  I always hoped that writing to a
webpage would be just as simple.  Unfortunately, with all of the new
JS frameworks (and security requirements), it has only gotten harder
to create basic, good-looking, functional UI.  Creating a
UI/dashboard, even for a for a small command-line script, requires
working in multiple languages, configuring multiple frameworks, and
hosting a FE stack -- all things that might take more code than your
original script -- and it takes hours (if not days) to get it all up
and running!  On the other hand, the *only* way for non-engineers to
interact with your code is through the web.  So you either pay all the
cost to make an interface, or you don't.  I built Dashborg so you
don't have to make that choice.  Maybe then we could get more tools
online faster and get back to the real work of adding functionality,
not wrestling with and configuring FE technologies.
