package dashutil

import (
	"fmt"
	"regexp"
	"strings"
)

// keep in sync with dash consts
const (
	rootAppPath       = "/_/apps"
	appRuntimeSubPath = "/_/runtime"
)

func PathNoFrag(fullPath string) (string, error) {
	pathNs, path, _, err := ParseFullPath(fullPath, true)
	if err != nil {
		return "", err
	}
	pathNsStr := ""
	if pathNs != "" {
		pathNsStr = "/@" + pathNs
	}
	return pathNsStr + path, nil
}

var pathAppNameRe = regexp.MustCompile("^" + rootAppPath + "/([a-zA-Z][a-zA-Z0-9_.-]*)(?:/|$)")

func AppNameFromPath(path string) string {
	matches := pathAppNameRe.FindStringSubmatch(path)
	if matches == nil {
		return ""
	}
	return matches[1]
}

type SimplifyPathOpts struct {
	AppName string
}

func appPathFromName(appName string) string {
	return rootAppPath + "/" + appName
}

func SimplifyPath(fullPath string, opts *SimplifyPathOpts) string {
	if opts == nil {
		opts = &SimplifyPathOpts{}
	}
	pathNs, path, pathFrag, err := ParseFullPath(fullPath, true)
	if err != nil || pathNs != "" {
		return fullPath
	}
	pathAppName := AppNameFromPath(path)
	if pathAppName == "" {
		return fullPath
	}
	rtnPath := ""
	appPath := appPathFromName(pathAppName)
	if pathAppName == opts.AppName {
		path = strings.Replace(path, appPath, "", 1)
		rtnPath = "/@app"
	} else {
		path = strings.Replace(path, appPath, "", 1)
		rtnPath = fmt.Sprintf("/@app=%s", pathAppName)
	}
	fragStr := ""
	if pathFrag != "" {
		fragStr = ":" + pathFrag
	}
	if path == appRuntimeSubPath && pathFrag != "" {
		return rtnPath + fragStr
	}
	return rtnPath + path + fragStr
}

func ParseFullPath(fullPath string, allowFrag bool) (string, string, string, error) {
	if fullPath == "" {
		return "", "", "", fmt.Errorf("Path cannot be empty")
	}
	if len(fullPath) > FullPathMax {
		return "", "", "", fmt.Errorf("Path too long")
	}
	if fullPath[0] != '/' {
		return "", "", "", fmt.Errorf("Path must begin with '/'")
	}
	match := fullPathRe.FindStringSubmatch(fullPath)
	if match == nil {
		return "", "", "", fmt.Errorf("Invalid Path '%s'", fullPath)
	}
	path := match[2]
	if path == "" {
		path = "/"
	}
	if match[3] != "" && !allowFrag {
		return "", "", "", fmt.Errorf("Path does not allow path-fragment")
	}
	return match[1], path, match[3], nil
}

func ValidateFullPath(fullPath string, allowFrag bool) error {
	_, _, _, err := ParseFullPath(fullPath, allowFrag)
	return err
}
