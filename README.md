# Dashborg Go SDK

Dashborg is Frontend as a Service (FEaaS) built for backend and devops engineers to quickly create secure bite-sized internal tools, status pages, and reports.

Dashborg saves you time by handling all of the tedious details of frontend app creation and deployment (hosting, security, authentication, transport, UI libraries, JavaScript frameworks, CSS frameworks, etc.).

**Static Apps** allow you to send HTML, data, images, and CSV files to the Dashborg Service from anywhere, including cron jobs and serverless functions (AWS Lambda or Google Cloud Functions).  Great for reports or showing the status of scripts or jobs that run on backend servers.

**Connected Apps** allow you to connect any backend server to the Dashborg service (using an *outbound* connection).  Once connected, your backend can receive and respond to UI events (button clicks, form submissions, dropdown selections, etc.).  Connected apps are great for creating live status pages, admin tools, query interfaces, and configuration tools.

Dashborg is easy to get started with.  You can have your first app deployed in 5-minutes (no account/registration required).  Free tier covers most simple use cases.

## Dashborg Hello World

Get the SDK:
```
go get github.com/sawka/dashborg-go-sdk
```

Connect to the Dashborg Cloud Service (no account required):
```golang
config := &dashcloud.Config{AutoKeygen: true, AnonAcc: true}
client, _ := dashcloud.MakeClient(config)
```

Open your first app, and allow offline access:
```golang
app, _ := client.OpenApp("hello-world")
app.SetOfflineModeType(dash.OfflineModeEnable)
```

Create a declarative HTML view to render in your App.  Most HTML is supported as-is,
with Dashborg controls are implemented with custom tags (d-foreach, d-if, d-button, d-table, etc.).
Use &lt;app&gt; as your top-level tag.  Setting ```ui="dashborg"``` imports the Dashborg UI controls and styles.
Save as "hello-world.html":
```html
<app ui="dashborg">
  <h1>Hello World</h1>
</app>
```

Set your app's HTML (can also be set using a raw string using app.SetHtml):
```
app.SetHtmlFromFile("./hello-world.html")
```

Write the app to the Dashborg Cloud Service:
```
client.WriteApp(app)
```

Here's the complete code:
```golang
package main

import (
    "github.com/sawka/dashborg-go-sdk/pkg/dash"
    "github.com/sawka/dashborg-go-sdk/pkg/dashcloud"
)

func main() {
    config := &dashcloud.Config{AutoKeygen: true, AnonAcc: true}
    client, _ := dashcloud.MakeClient(config)
    app, _ := client.OpenApp("hello-world")
    app.SetOfflineModeType(dash.OfflineModeEnable)
    app.SetHtmlFromFile("./hello-world.html")
    client.WriteApp(app)
}
```

Run your code, and you'll see a secure link to your app that includes a JWT token that will grant access.
Copy and paste (or click) on the link to see your first Dashborg App!

### Connected Apps

Now let's create a simple interactive app.  Here's how to create a button that calls a backend function
and displays it's output:

First add a button with an onclick handler to your UI.  Handlers are like URLs and start with "/".  Here
we've hooked up a Dashborg Button control to the handler "/test-handler".  Using the Dashborg Button makes
it look nice, but you can add a click handler to any HTML element.  The ```d-dataview``` element can
show data as formatted JSON:
```html
  <app ui="dashborg">
    <d-button onclickhandler="$.output = /test-handler">Run Test Handler</d-button>
    <d-dataview bind="$.output"/>
  </app>
```

When you click the button you'll get an error since we don't have any handlers registered.

Let's modify our backend code
to add our handler.  First we'll add our handler function to our app's Runtime().  Then instead of calling
WriteApp(), we'll call ConnectApp() to connect the runtime.  WaitForShutdown() will keep our program
running while we listen for events.

```golang
func TestFn() (interface{}, error) {
    fmt.Printf("Calling TestFn!\n")
    return map[string]interface{}{"success": true, "message": "TestFn Output!"}, nil
}

func main() {
    config := &dashcloud.Config{AutoKeygen: true, AnonAcc: true}
    client, _ := dashcloud.MakeClient(config)
    app, _ := client.OpenApp("hello-world")
    app.SetOfflineModeType(dash.OfflineModeEnable)
    app.SetHtmlFromFile("./hello-world.html")
    app.Runtime().Handler("/test-handler", TestFn)
    client.ConnectApp(app)
    client.WaitForShutdown()
}
```

Run the code, click the button and you'll see "Calling TestFn!" appear in your console and
the return value will appear in the Dashborg UI!

AFAIK this is the simplest and quickest way to get a publically accessible, secure, button connected to backend
code.

### Adding BLOBs

Want to show an image, or add a CSV file your end users to download, here's how to do it:
```
    // first argument is the key-name of the BLOB in your app, 2nd is mime-type, 3rd is filename
    app.Blobs().SetBlobDataFromFile("myimage.jpg", "image/jpeg", "./path-to-image.jpg")
    app.Blobs().SetBlobDataFromFile("data.csv", "text/csv", "./path-to-csv-on-filesystem.csv")
```

Show the image using a regular &lt;img&gt; tag in your HTML template.  Using a ```*``` as the first character in any HTML attribute makes it dynamic.  BLOB metadata is held in the special frontend variable ```$blobs```.
```
    <img src="* $blobs['myimage.jpg'].url"/>
```

Download the CSV using a standard HTML download link:
```
    <a href="* $blobs['data.csv'].url" download>Download CSV</a>
```

Or use a Dashborg download control to make it look nice (defined in the standard Dashborg UI package):
```
    <d-download blob="data.csv">Download CSV</d-download>
```

### Adding Static Data

Dashborg uses JSON to transfer data between your app and the Dashborg service.  You can send any
static JSON-compatible data to Dashborg using app.Blobs().SetJsonBlob().  Static data is available to
apps even when there is no backend connected.  For dynamic data, use Runtime().Handler().
Here we'll send an HTML color table as a BLOB named "colors":
```golang
type FavColor struct {
    Name  string `json:"name"`
    Color string `json:"color"`
    Hex   string `json:"hex"`
}
...
colors := make([]FavColor, 0)
colors = append(colors, FavColor{"Mike", "blue", "#007fff"})
colors = append(colors, FavColor{"Chris", "red", "#ee0000"})
colors = append(colors, FavColor{"Jenny", "purple", "#a020f0"})
app.Blobs().SetJsonBlob("colors", colors, nil)
```

Load the data into our datamodel using the &lt;d-data&gt; tag.  Read from blob "colors", set it into the 
frontend data model at ```$.colors```:
```html
<d-data blob="colors" output.bindpath="$.colors"/>
```

Show the first color name as text using ```<d-text>```.  Use the hex color to show a
small color square using a background-color style (style is dynamic when it starts with ```*```):
```html
<div>
    <d-text bind="$.colors[0].name"/>'s favorite color is <d-text bind="$.colors[0].color"/>
    <div style="width: 100px; height: 100px; background-color: *$.color[0].hex"/>
</div>
```

You can loop using the built-in ```<d-foreach>``` tag (each element is bound to ```.``` inside the loop):
```html
<ul class="ui bulleted list">
    <d-foreach bind="$.colors">
      <li class="item" style="height: 24px">
        <div class="row">
          <div><d-text bind=".name"/> - Favorite Color is <d-text bind=".color"/></div>
          <div style="width: 18px; height: 18px; background-color: * .hex"/>
        </div>
      </li>
    </d-foreach>
</ul>
```

Or use a Dashborg Table Control (@index is bound to the loop counter):
```html
<d-table bind="$.colors">
   <d-col label="#" bind="@index+1"/>
   <d-col label="Name" bind=".name"/>
   <d-col label="Color" bind=".color"/>
   <d-col label="Swatch">
       <div style="width: 100px; height: 100px; background-color: * .hex"/>
   </d-col>
</d-table>
```

### Advanced Handlers / Forms

Dashborg handlers are registered with reflection.  The first argument is an optional
```*dash.Request``` struct.  The rest of the arguments come from the frontend code.  Functions 
return void, interface{}, error, or (interface{}, error).  Errors are shown in the application (or handled by special
error handlers), and the interface{} return value can be consumed by the calling code.

Handlers can also manipulate the frontend directly (aside from their return value) by calling
SetData() to set or change values in the frontend data-model.

Here's a handler that manipulates the frontend's data model (data is automatically marshaled as JSON):
```golang
func Multiply2(req *dash.Request, num int) error {
    req.SetData("$.output", num*2)
    return nil
}

...
app.Runtime().Handler("/mult2", Multiply2)
```

Now we'll use a button to call the function, and a div to show the return color.  Note that HTML inputs
produce strings, so we must convert the string to a number using fn:int().
```html
<app>
    <div class="row">
        <d-input type="number" min="0" max="100" value.bindpath="$.inputnumber" defaultvalue="0"/>
        <d-button onclickhandler="/mult2(fn:int($.inputnumber))">Multiply</d-button>
    </div>
    <div>
        Output is <d-text bind="$.output || 0"/>
    </div>
</app>
```

## Security

All communication from your backend to the Dashborg service is done over HTTPS/gRPC.  Your account is authenticated
with a public/private keypair that can be auto-generated by the Dashborg SDK (AutoKeygen config setting).

The frontend is served over HTTPS, and each account is hosted on its own subdomain to prevent inter-account XSS attacks 
The Dashborg frontend offers pre-built authentication methods, with JWT tokens that are
created from your private-key (the default for new anonymous accounts), simple passwords, or user logins.

## Advanced Features

* Write your own Dashborg components to reuse among your applications
* Create staging/development zones to test your apps without affecting your production site
* Assign roles to users (and passwords), set a list of allowed roles per app per zone
* Stream data and updates from your backend to users
* (Coming Soon) Create app instances to connect to individual backend servers -- great for managing individual nodes in a cluster or creating a log of every script run.

## Want to learn more?

These resources are currently being updated for the new v0.6 Dashborg release.  Currently they
refer to the older v0.5 release and are out of date.

* <s>**Doc Site**: https://docs.dashborg.net/</s>
* <s>**Examples** (golang directory): https://github.com/sawka/dashborg-examples</s>
* <s>**Tutorial**: https://docs.dashborg.net/tutorials/t1/</s>
* <s>**Binding Data to Your HTML**: https://docs.dashborg.net/docs/binding-data/</s>
* <s>**GoDoc**: https://pkg.go.dev/github.com/sawka/dashborg-go-sdk/pkg/dash</s>
* <s>**Demo Site**: https://acc-421d595f-9e30-4178-bcc3-b853f890fb8e.console.dashborg.net/zone/default/default</s>

Questions?  [Join the Dashborg Slack Channel](https://join.slack.com/t/dashborgworkspace/shared_invite/zt-uphltkhj-r6C62szzoYz7_IIsoJ8WPg)

