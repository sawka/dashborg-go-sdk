# Dashborg Go SDK

Dashborg is an alternative web framework to create and deploy bite-sized internal tools without overhead, scaffolding, or JavaScript!

## Why?

Creating good looking, modern, secure admin tools should be easy.  But, there is a huge amount of overhead, configuration, and scaffolding to set up a JavaScript stack, add security/authentication, and create good looking UI components.  Then there is the operational overhead of deployment (opening ports, domain names, and load balancers).

Simple tools might only need 50-lines of real application logic, but your application server code, UI templates, and configuration files can add 10-20 new files and directories!  If you set up a JavaScript stack, you'll add a package.json, webpack.config, .babelrc, and literally *hundreds of megabytes* of node_modules just to run ReactJS.

This is crazy.

Dashborg is simple.  One HTML file (which can be inlined) for your UI and a couple of lines of code
to create handlers to call your backend functions.  And, if you use the Dashborg Cloud service, you get
https, pluggable authentication, and instant deployment to a secure public URL.

## Dashborg Hello World

First create an App:
```golang
app := dash.MakeApp("hello-world")
```

Then, create some handlers for actions you want to run, or data you want to view:
```golang
app.Handler("/run-action", func(req *dash.Request) error {
    fmt.Printf("running backend action\n")
    return nil
})
```

Create a declarative HTML view to render your App and call your handlers.  You can use
any regular HTML, or pre-styled Dashborg components (they have the ```d-``` prefix):

```golang
<panel>
  <h1>Demo Dashboard</h1>
  <d-button handler="/run-action">Run Action</d-button>
</panel>
```

Set this HTML in your app as a string or save it in a file:
```golang
app.SetHtml(PANEL_HTML)                 // pass as a string
app.SetHtmlFromFile("hello-world.html") // or pass a filename
```

Connect your App to the local-container to view it:

```golang
container, _ := dashlocal.MakeContainer(nil)
container.ConnectApp(app)
container.WaitForShutdown() // runs until the container is shutdown
```

View/test your app at [http://localhost:8082](http://localhost:8082)!

## Dashborg Cloud

Want to deploy your app in the cloud?  Change the container to
the dashcloud container.  No registration is required, a new account id, and public/private keypair will be
auto provisioned (when using the AutoKeygen flag in the config).

```golang
cfg := &dashcloud.Config{
    AnonAcc:    true,
    AutoKeygen: true,
}
container, _ := dashcloud.MakeClient(cfg)
container.ConnectApp(app)
container.WaitForShutdown()
```

You should see output that looks similar to:

<pre style="font-size: 14px; line-height: normal; overflow-x: scroll;">
Dashborg created new self-signed keypair [dashborg-client.key, dashborg-client.crt] for AccId:<b>[YOUR ACCOUNT ID]</b>
Dashborg KeyFile:dashborg-client.key CertFile:dashborg-client.crt SHA256:<b>[PUBLIC KEY HASH]</b>
Dashborg Initialized CloudClient AccId:<b>[YOUR ACCOUNT ID]</b> Zone:default ProcName:demo ProcRunId:221e5a4c-51ff-4921-81ad-a9702a9e8583
Dashborg CloudContainer App Link [todo]: <b>[SECURE LINK TO YOUR APP]</b>
</pre>

Your application communicates with the Dashborg cloud with public/private key authentication, and the frontend is secured by default with a JWT token, verified with your new public/private keypair.  The Dashborg cloud can host multiple applications and supports multiple types of pluggable authentication.

Full Code to the Hello World example in the golang directory of the dashborg-examples repository:
[https://github.com/sawka/dashborg-examples/tree/master/golang](https://github.com/sawka/dashborg-examples/tree/master/golang)

## Want to learn more?

* **Doc Site**: https://docs.dashborg.net/
* **Examples** (golang directory): https://github.com/sawka/dashborg-examples
* **Tutorial**: https://docs.dashborg.net/tutorials/t1/
* **Binding Data to Your HTML**: https://docs.dashborg.net/docs/binding-data/
* **GoDoc**: https://pkg.go.dev/github.com/sawka/dashborg-go-sdk/pkg/dash
* **Demo Site**: https://acc-421d595f-9e30-4178-bcc3-b853f890fb8e.console.dashborg.net/zone/default/default

Questions?  [Join the Dashborg Slack Channel](https://join.slack.com/t/dashborgworkspace/shared_invite/zt-ls710ixw-nHmCAFiOQqzal2mu0r_87w)
