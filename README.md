# Dashborg Go SDK

Modern internal tools.  Defined, controlled, and deployed directly from backend code.  No JavaScript.  Secure.
* Define your UI in HTML
* Define handlers in Go/Python
* Run your program to deploy your app

Dashborg was built to be the simplest way to create secure web-based internal tools from backend code.  Define your UI and control it *completely* within your backend code.  All backend communication is secured with public/private key encryption.  Add client password or user-account authentication with one line of code.

## Documentation

* Doc Site: https://docs.dashborg.net/
* Tutorials: https://docs.dashborg.net/tutorials/t1/
* GoDoc (pkg.go.dev): https://pkg.go.dev/github.com/sawka/dashborg-go-sdk/pkg/dash
* Playground: https://console.dashborg.net/acc/421d595f-9e30-4178-bcc3-b853f890fb8e/default/playground
* Slack: [Join the Dashborg Slack Channel](https://join.slack.com/t/dashborgworkspace/shared_invite/zt-ls710ixw-nHmCAFiOQqzal2mu0r_87w)

## Key Features

* **No Javascript** - No context switching.  Write your dashboards using pure HTML and backend code.  No JavaScript environment to set up, no messing with NPM, Yarn, Webpack, Babel, React, Angular, etc.
* **No Open Ports** - No webhooks, firewall configuration, IP whitelists, or open ports required for your backend.
* **No Frontend Hosting** - You get a secure, internet accessible frontend out of the box.  No web server configuration, domain name, load balancer, or WAF setup and configuration required.
* **No Shared Passwords** - No incoming connections to your infrastructure.  Dashborg does not require or store your database passwords or API keys.  It does not access any 3rd party service on your behalf.
* **Built For Real Developers** - Use the editor, libraries, and frameworks that you already use to write your tools -- no 3rd-party GUI tools to learn, or typing code into text boxes on a website.  Easy to get started, but powerful enough to build complex tools and interactions.
* **Secure** - All backend connections are secured using SSL public/private key encryption with client auth.  HTTPS on the frontend.  Secure your dashboards with a simple password or user accounts.  SSO coming soon.
* **Control** - Dashborg panels are 100% defined from your backend code.  That means you can version them in your own code repository, and run and test them in your current dev, staging, and production environments.
* **Modern Frontend Controls** - Tables, Lists, Forms, Inputs, and Buttons all out of the box, with more to come!  No Javascript or CSS frameworks required.  All styled to look good and work together.

## Dashborg Hello World

The code below is the complete code for your first Dashborg program.
Copy and paste it into a new go file "demo.go" and run it: ```go run demo.go```.
Click the Dashborg Panel Link to view your live, deployed dashboard.

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

You should see output that looks similar to:

<pre style="font-size:12px; line-height: normal; overflow-x: scroll;">
Dashborg created new self-signed keypair key:dashborg-client.key cert:dashborg-client.crt for new accountid:[YOUR-ACCOUNT-ID]
Dashborg KeyFile:dashborg-client.key CertFile:dashborg-client.crt SHA256:[YOUR-KEY-FINGERPRINT]
Dashborg Initialized Client AccId:[YOUR-ACCOUNT-ID] Zone:default ProcName:demo ProcRunId:4f4e8364-5d39-495f-8c9e-1009741b1b47
Dashborg Panel Link [default]: https://console.dashborg.net/acc/YOUR-OWN-PRIVATE-LINK

</pre>

Copy and paste your panel link (console.dashborg.net) into your browser to see your live dashboard!

Questions?  [Join the Dashborg Slack Channel](https://join.slack.com/t/dashborgworkspace/shared_invite/zt-ls710ixw-nHmCAFiOQqzal2mu0r_87w)
