package dashcloud

import (
	"regexp"
	"strings"

	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

var limitErrorRE = regexp.MustCompile("DashborgLimitError limit:([a-zA-Z0-9]+)(?:\\.([a-zA-Z0-9]+))?")

type limitKey struct {
	AccType      string
	LimitName    string
	SubLimitName string
}

var limitExplanations map[limitKey]string

func init() {
	limitExplanations = make(map[limitKey]string)

	limitExplanations[limitKey{dash.AccTypeAnon, "MaxApps", ""}] = "Anonymous Dashborg accounts only support one app.  Register your account for free to enable up to 5 apps."
	limitExplanations[limitKey{dash.AccTypeAnon, "MaxAppsPerZone", ""}] = "Anonymous Dashborg accounts only support one app.  Register your account for free to enable up to 5 apps."
	limitExplanations[limitKey{dash.AccTypeAnon, "MaxZones", ""}] = "Anonymous Dashborg accounts only support a single zone.  Register and upgrade to a PRO account to enable multiple zones"
	limitExplanations[limitKey{dash.AccTypeAnon, "AppBlobs", ""}] = "Anonymous Dashborg accounts have very limited BLOB storage limits (to prevent abuse).  Register your account for free to enable larger BLOB sizes and storage limits"
	limitExplanations[limitKey{dash.AccTypeAnon, "AllBlobs", ""}] = "Anonymous Dashborg accounts have very limited BLOB storage limits (to prevent abuse).  Register your account for free to enable larger BLOB sizes and storage limits"
	limitExplanations[limitKey{dash.AccTypeAnon, "HtmlSize", ""}] = "Anonymous Dashborg accounts have a limit on HTML size.  Register your account for free enable a larger HTML size"
	limitExplanations[limitKey{dash.AccTypeAnon, "BackendTransferMB", ""}] = "Anonymous Dashborg accounts have a very limited data transfer allowance (to prevent abuse).  Register your account for free to enable much higher transfer limits"

	limitExplanations[limitKey{dash.AccTypeFree, "MaxApps", ""}] = "Free Dashborg accounts only support up to 5 apps.  Upgrade to a PRO account to enable up to 20 apps per zone, a staging zone, increased storage limits, and more user accounts."
	limitExplanations[limitKey{dash.AccTypeFree, "MaxAppsPerZone", ""}] = "Free Dashborg accounts only support up to 5 apps.  Upgrade to a PRO account to enable up to 20 apps per zone, a staging zone, increased storage limits, and more user accounts."
	limitExplanations[limitKey{dash.AccTypeFree, "MaxZones", ""}] = "Free Dashborg accounts only support a single zone.  Register and upgrade to a PRO account to enable multiple zones, more apps, increased storage limits, and more user accounts."
	limitExplanations[limitKey{dash.AccTypeFree, "AppBlobs", ""}] = "Free Dashborg accounts have limited BLOB storage limits.  Upgrade to a PRO account for much larger BLOB storage limits (up to 10G)"
	limitExplanations[limitKey{dash.AccTypeFree, "HtmlSize", ""}] = "Free Dashborg accounts have a limit on HTML size.  Upgrade to a PRO account for a larger HTML limit"
	limitExplanations[limitKey{dash.AccTypeAnon, "BackendTransferMB", ""}] = "Free Dashborg accounts have a limited data transfer allowance.  Upgrade to a PRO account to increase your transfer limit"

}

func (pc *DashCloudClient) explainLimit(accType string, errMsg string) {
	if accType != dash.AccTypeAnon && accType != dash.AccTypeFree {
		return
	}
	if strings.Index(errMsg, "DashborgLimitError") == -1 {
		return
	}
	match := limitErrorRE.FindStringSubmatch(errMsg)
	if match == nil {
		return
	}
	limitName := match[1]
	explanation := limitExplanations[limitKey{accType, limitName, ""}]
	if explanation != "" {
		pc.log("%s\n", explanation)
	}
}
