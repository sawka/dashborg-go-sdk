# Dashborg Go SDK

Dashborg is a SDK that plugs directly into any backend service or script.  With a couple lines of code you can register any running function with the Dashborg service and then build secure, modern, interactive tools on top of those functions using a simple, JavaScript free, HTML template language.

Dashborg saves you time by handling all of the tedious details of frontend app creation and deployment (hosting, end-to-end security, authentication, transport, UI libraries, JavaScript frameworks, CSS frameworks, etc.).

Dashborg works great for debugging, introspecting the running state of servers, viewing/updating configuration values, bite-sized internal tools, status pages, and reports.

Dashborg is easy to get started with.  You can have your first app deployed in 5-minutes (no account/registration required).  Free tier covers most simple use cases.

* GoDoc (pkg.go.dev) -  https://pkg.go.dev/github.com/sawka/dashborg-go-sdk/pkg/dash
* Dashborg Documentation Site - https://docs.dashborg.net/
* Tutorial - https://docs.dashborg.net/tutorials/t1/

Questions? [Join the Dashborg Slack Channel](https://join.slack.com/t/dashborgworkspace/shared_invite/zt-uphltkhj-r6C62szzoYz7_IIsoJ8WPg)

## Dashborg Hello World

```Golang
package main

import (
    "fmt"

    "github.com/sawka/dashborg-go-sdk/pkg/dash"
)

var counter int

func TestFn() (interface{}, error) {
    counter++
    fmt.Printf("Calling TestFn counter:%d!\n", counter)
    return map[string]interface{}{"success": true, "message": "TestFn Output!", "counter": counter}, nil
}

func main() {
    // Configure and connect the Dashborg Client
    config := &dash.Config{AutoKeygen: true, AnonAcc: true}
    client, err := dash.ConnectClient(config)
    if err != nil {
        fmt.Printf("Error connecting DashborgCloudClient: %v\n", err)
        return
    }

    // Create a new app called "hello-world"
    app := client.AppClient().NewApp("hello-world")
    // Set the UI for our app
    app.WatchHtmlFile("./hello-world.html", nil)
    // Register a handler function (that can be called directly from the UI)
    app.Runtime().PureHandler("test-handler", TestFn)
    // Deploy the app, check for errors
    err = client.AppClient().WriteAndConnectApp(app)
    if err != nil {
        fmt.Printf("Error writing app: %v\n", err)
        return
    }
    // Wait forever while your program is responding to handler requests
    client.WaitForShutdown()
}
```

The HTML template to render your app (save as hello-world.html):
```HTML
<app ui="dashborg">
  <h1>Hello World</h1>
  <div class="row xcenter">
    <d-button onclickhandler="$.output = /@app:test-handler">Run Test Handler</d-button>
    <d-stat label="Counter" bind="$.output.counter" defaultvalue="0"/>
  </div>
  <hr/>
  <d-dataview bind="$.output"/>
</app>
```

Run your program.  A new public/private keypair will be created and used to secure your new Dashborg account.  You'll also see a secure link to your new application.  Click to view your application!

That's it.  You've created and deployed, secure, interactive web-app that is directly calling code running on your local machine!

## Adding BLOBs

Want to show an image, or add a CSV file your end users to download, here's how to do it:
```
    client.GlobalFSClient().SetPathFromFile("/image/myimage.jpg", "./path-to-image.jpg", &dash.FileOpts{MimeType: "image/jpeg"})
    client.GlobalFSClient().SetPathFromFile("/mydata.csv", "./path-to-csv.csv", &dash.FileOpts{MimeType: "text/csv"})
```

Show the image using a regular &lt;img&gt; tag in your HTML template.  Using the path prefix "/@raw/"
allows for raw http GET access to your uploaded content:
```
    <img src="/@raw/image/myimage.jpg" style="max-width: 500px;"/>
```

Download the CSV using a standard HTML download link:
```
    <a href="/@raw/mydata.csv" download>Download CSV</a>
```

Or use a Dashborg download control (defined in the standard Dashborg UI package) to make it look nice:
```
    <d-download path="/mydata.csv">Download CSV</d-download>
```

## Adding Static Data

Dashborg uses JSON to transfer data between your app and the Dashborg service.  You can send any
static JSON-compatible data to Dashborg using app.Blobs().SetJsonBlob().  Static data is available to
apps even when there is no backend connected.  For dynamic data, use Runtime().Handler().
Here we'll set favorite color table to the path "/colors.json".  We used the AppFSClient() instead of
the GlobalFSClient().  That makes the data local to the app and is accessible at /@app/colors.json:
```golang
type FavColor struct {
    Name  string `json:"name"`
    Color string `json:"color"`
    Hex   string `json:"hex"`
}

colors := make([]FavColor, 0)
colors = append(colors, FavColor{"Mike", "blue", "#007fff"})
colors = append(colors, FavColor{"Chris", "red", "#ee0000"})
colors = append(colors, FavColor{"Jenny", "purple", "#a020f0"})
app.AppFSClient().SetJsonPath("/colors.json", colors, nil)
```

Load the data into our datamodel using the &lt;d-data&gt; tag.  Read from blob "colors", set it into the 
frontend data model at ```$.colors```:
```html
<d-data query="/@app/colors.json" output.bindpath="$.colors"/>
```

Show the first color name as text using ```<d-text>```.  Use the hex color to show a
small color square using a background-color style (attributes and styles are dynamic when they starts with ```*```):
```html
<div>
    <d-text bind="$.colors[0].name"/>'s favorite color is <d-text bind="$.colors[0].color"/>:
    <div style="display: inline-block; vertical-align: bottom; width: 18px; height: 18px; background-color: *$.colors[0].hex"/>
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
       <div style="width: 50px; height: 50px; background-color: * .hex"/>
   </d-col>
</d-table>
```

## Advanced Handlers / Forms

Dashborg handlers are registered with reflection.  The first argument is an optional ```dash.Request``` interface, ```*dash.AppRequest``` struct, or ```context.Context```.  The rest of the arguments come from the frontend code.  Functions return void, interface{}, error, or (interface{}, error).  Errors are shown in the application (or handled by special error handlers), and the interface{} return value can be consumed by the calling code.

Handlers that use ```*dash.AppRequest``` can also manipulate the frontend directly (aside from their return value) by calling SetData() to set or change values in the frontend data-model.

Here's a handler that manipulates the frontend's data model (data is automatically marshaled as JSON):
```golang
func Multiply2(req *dash.AppRequest, num int) error {
    req.SetData("$.output", num*2)
    return nil
}

// ...

app.Runtime().Handler("mult2", Multiply2)
```

Now we'll use a button to call the function, and a div to show the return value.  Note that HTML inputs
produce strings, so we must convert the string to a number using fn:int().
```html
<app>
    <div class="row">
        <d-input type="number" min="0" max="100" value.bindpath="$.inputnumber" defaultvalue="0"/>
        <d-button onclickhandler="/@app:mult2(fn:int($.inputnumber))">Multiply</d-button>
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
* Navigate to the '/@fs' path on your Dashborg account to see all of your static data, applications, and handlers.

## Want to learn more?

* **Doc Site**: https://docs.dashborg.net/
* **Tutorial**: https://docs.dashborg.net/tutorials/t1/
* **Binding Data to Your HTML**: https://docs.dashborg.net/docs/binding-data/
* **GoDoc**: https://pkg.go.dev/github.com/sawka/dashborg-go-sdk/pkg/dash

Questions?  [Join the Dashborg Slack Channel](https://join.slack.com/t/dashborgworkspace/shared_invite/zt-uphltkhj-r6C62szzoYz7_IIsoJ8WPg)


