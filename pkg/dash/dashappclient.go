package dash

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/sawka/dashborg-go-sdk/pkg/dasherr"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

const (
	AppRuntimeSubPath = "/_/runtime"
	AppHtmlSubPath    = "/_/html"
)

type DashAppClient struct {
	client *DashCloudClient
}

// Create a new app with the given name.  The app is just created
// client side, it is not written to the server.
// Invalid names will be reported in App.Err()
func (dac *DashAppClient) NewApp(appName string) *App {
	return makeApp(dac.client, appName)
}

// Create a new app from the passed in AppConfig.  Config must be valid and complete.
// Normal end users should use LoadApp(), not this function.
func (dac *DashAppClient) NewAppFromConfig(cfg AppConfig) (*App, error) {
	return makeAppFromConfig(dac.client, cfg)
}

// Tries to load an app from the Dashborg service with the given name.  If no existing
// app is found then if createIfNotFound is false will return nil, nil.  If createIfNotFound
// is true, will return NewApp(appName).
func (dac *DashAppClient) LoadApp(appName string, createIfNotFound bool) (*App, error) {
	appPath := AppPathFromName(appName)
	finfos, _, err := dac.client.fileInfo(appPath, nil, false)
	if err != nil {
		return nil, err
	}
	if finfos == nil || len(finfos) == 0 {
		if createIfNotFound {
			return dac.NewApp(appName), nil
		}
		return nil, nil
	}
	finfo := finfos[0]
	if finfo.FileType != FileTypeApp || finfo.AppConfigJson == "" {
		return nil, nil
	}
	var config AppConfig
	err = json.Unmarshal([]byte(finfo.AppConfigJson), &config)
	if err != nil {
		return nil, err
	}
	return makeAppFromConfig(dac.client, config)
}

// Writes the app to the Dashborg service.  Note that the app runtime will
// *not* be connected.  This is used to create or update an app's settings,
// offline apps, or apps with external runtimes.
func (dac *DashAppClient) WriteApp(app *App) error {
	return dac.baseWriteApp(app, false)
}

// Writes the app to the Dashborg service and connects the app's runtime to
// receive requests.  If an app uses an external runtime, you should call
// WriteApp(), not WriteAndConnectApp().
func (dac *DashAppClient) WriteAndConnectApp(app *App) error {
	return dac.baseWriteApp(app, true)
}

// Given an app name, returns the canonical path (e.g. /_/apps/[appName])
func AppPathFromName(appName string) string {
	return RootAppPath + "/" + appName
}

// Removes the app and any static data/runtimes/files under the canonical app root.
// So any file that was created using the AppFSClient() will also be removed.
// Any connected runtimes will also be disconnected.
func (dac *DashAppClient) RemoveApp(appName string) error {
	if !dashutil.IsAppNameValid(appName) {
		return dasherr.ValidateErr(fmt.Errorf("Invalid App Name"))
	}
	err := dac.client.removeAppPath(appName)
	if err != nil {
		return err
	}
	return nil
}

// Connects the app's runtime without modifying any settings (does not write
// the app config to the Dashborg service).  Note that a different WriteApp
// or WriteAndConenctApp must have already created the app.
func (dac *DashAppClient) ConnectAppRuntime(app *App) error {
	appConfig, err := app.AppConfig()
	if err != nil {
		return err
	}
	if app.Runtime() == nil {
		return dasherr.ValidateErr(fmt.Errorf("No AppRuntime to connect, app.Runtime() is nil"))
	}
	if app.HasExternalRuntime() {
		return dasherr.ValidateErr(fmt.Errorf("App has specified an external runtime path '%s', use DashFS().LinkAppRuntime() to connect", app.getRuntimePath()))
	}
	runtimePath := appConfig.RuntimePath
	err = dac.client.connectLinkRpc(appConfig.RuntimePath)
	if err != nil {
		return err
	}
	dac.client.connectLinkRuntime(runtimePath, app.Runtime())
	return nil
}

// Creates a URL to link to an app given its name.  Optional jwtOpts to override the
// config's default jwt options.
func (dac *DashAppClient) MakeAppUrl(appNameOrPath string, jwtOpts *JWTOpts) (string, error) {
	if appNameOrPath == "" {
		return "", fmt.Errorf("Invalid App Path")
	}
	if appNameOrPath[0] == '/' {
		return dac.client.GlobalFSClient().MakePathUrl(appNameOrPath, jwtOpts)
	}
	appName := appNameOrPath
	accHost := dac.client.getAccHost()
	baseUrl := accHost + dashutil.MakeAppPath(dac.client.Config.ZoneName, appName)
	if jwtOpts == nil {
		jwtOpts = dac.client.Config.GetJWTOpts()
	}
	if jwtOpts.NoJWT {
		return baseUrl, nil
	}
	jwtToken, err := dac.client.Config.MakeAccountJWT(jwtOpts)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s?jwt=%s", baseUrl, jwtToken), nil
}

func (dac *DashAppClient) baseWriteApp(app *App, shouldConnect bool) error {
	appConfig, err := app.AppConfig()
	if err != nil {
		return err
	}
	if shouldConnect && app.HasExternalRuntime() {
		return dasherr.ValidateErr(fmt.Errorf("App has specified an external runtime path '%s', use DashFS().LinkAppRuntime() to connect", app.getRuntimePath()))
	}
	roles := appConfig.AllowedRoles
	appConfigJson, err := dashutil.MarshalJson(appConfig)
	if err != nil {
		return dasherr.JsonMarshalErr("AppConfig", err)
	}
	fs := dac.client.GlobalFSClient()
	err = fs.SetRawPath(app.AppPath(), nil, &FileOpts{FileType: FileTypeApp, MimeType: MimeTypeDashborgApp, AllowedRoles: roles, AppConfigJson: appConfigJson}, nil)
	if err != nil {
		return err
	}
	// test html for error earlier
	htmlPath := appConfig.HtmlPath
	htmlFileOpts := &FileOpts{MimeType: MimeTypeHtml, AllowedRoles: roles}
	if app.htmlStr != "" {
		err = fs.SetStaticPath(htmlPath, bytes.NewReader([]byte(app.htmlStr)), htmlFileOpts)
	} else if app.htmlFileName != "" {
		if app.htmlFileWatchOpts == nil {
			err = fs.SetPathFromFile(htmlPath, app.htmlFileName, htmlFileOpts)
		} else {
			err = fs.WatchFile(htmlPath, app.htmlFileName, htmlFileOpts, app.htmlFileWatchOpts)
		}
	}
	if err != nil {
		return err
	}
	if shouldConnect {
		runtimePath := appConfig.RuntimePath
		err = fs.LinkAppRuntime(runtimePath, app.Runtime(), &FileOpts{AllowedRoles: roles})
		if err != nil {
			return err
		}
	}
	appLink, err := dac.MakeAppUrl(appConfig.AppName, nil)
	if err == nil {
		dac.client.log("Dashborg App Link [%s]: %s\n", appConfig.AppName, appLink)
	}
	return nil
}
