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
	appPathNs         = "app"
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

type FormatPathOpts struct {
	AppName string
	FsPath  string
}

func appPathFromName(appName string) string {
	return rootAppPath + "/" + appName
}

func CanonicalizePath(fullPath string, opts *FormatPathOpts) (string, error) {
	if opts == nil {
		opts = &FormatPathOpts{}
	}
	pathNs, path, pathFrag, err := ParseFullPath(fullPath, true)
	if err != nil {
		return "", err
	}
	if pathNs == "" {
		return fullPath, nil
	}
	fragStr := ""
	if pathFrag != "" {
		fragStr = ":" + pathFrag
	}
	if pathNs == "app" {
		if opts.AppName == "" {
			return "", fmt.Errorf("Cannot canonicalize /@app path without an app context (use canonical path or /@app=[appname]/ format)")
		}
		if path == "/" && pathFrag != "" {
			return fmt.Sprintf("/_/apps/%s/_/runtime:%s", opts.AppName, pathFrag), nil
		}
		return fmt.Sprintf("/_/apps/%s%s%s", opts.AppName, path, fragStr), nil
	}
	if pathNs == "self" {
		if opts.FsPath == "" {
			return "", fmt.Errorf("Cannot canonicalize /@self path without an FS path (use canonical path format)")
		}
		if path != "/" {
			return "", fmt.Errorf("Cannot /@self path cannot have sub-path")
		}
		return CanonicalizePath(fmt.Sprintf("%s%s", opts.FsPath, fragStr), opts)
	}
	if strings.HasPrefix(pathNs, "app=") {
		appName := pathNs[4:]
		if !IsAppNameValid(appName) {
			return "", fmt.Errorf("Cannot canonicalize /%s path, invalid app name", pathNs)
		}
		if path == "/" && pathFrag != "" {
			return fmt.Sprintf("/_/apps/%s/_/runtime:%s", appName, pathFrag), nil
		}
		return fmt.Sprintf("/_/apps/%s%s%s", appName, path, fragStr), nil
	}
	return fullPath, nil
}

func GetPathNs(fullPath string) string {
	pathNs, _, _, _ := ParseFullPath(fullPath, true)
	return pathNs
}

func SimplifyPath(fullPath string, opts *FormatPathOpts) string {
	if opts == nil {
		opts = &FormatPathOpts{}
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
	if strings.Index(path, "//") != -1 {
		return "", "", "", fmt.Errorf("Path '%s' contains empty part, '//' in path pos=%d", path, strings.Index(path, "//"))
	}
	return match[1], path, match[3], nil
}

func ValidateFullPath(fullPath string, allowFrag bool) error {
	_, _, _, err := ParseFullPath(fullPath, allowFrag)
	return err
}

func GetFileName(fullPath string) (string, error) {
	_, pathMain, _, err := ParseFullPath(fullPath, true)
	if err != nil {
		return "", err
	}
	if pathMain == "" {
		return "", fmt.Errorf("Invalid path")
	}
	if pathMain == "/" {
		return pathMain, nil
	}
	lastIndex := -1
	if pathMain[len(pathMain)-1] == '/' {
		lastIndex = strings.LastIndex(pathMain[0:len(pathMain)-1], "/")
	} else {
		lastIndex = strings.LastIndex(pathMain, "/")
	}
	if lastIndex == -1 {
		return "", fmt.Errorf("Invalid path")
	}
	return pathMain[lastIndex+1:], nil
}

func GetParentDirectory(fullPath string) (string, error) {
	pathNs, pathMain, _, err := ParseFullPath(fullPath, true)
	if err != nil {
		return "", err
	}
	if pathMain == "/" {
		return "", fmt.Errorf("Root directory does not have parent")
	}
	if pathMain[len(pathMain)-1] == '/' {
		// remove trailing '/' (directory)
		pathMain = pathMain[0 : len(pathMain)-1]
	}
	lastIndex := strings.LastIndex(pathMain, "/")
	if lastIndex == -1 {
		return "", fmt.Errorf("Invalid Path '%s' (does not start with /)", fullPath)
	}
	parentPath := pathMain[0 : lastIndex+1]
	nsStr := ""
	if pathNs != "" {
		nsStr = "/@" + pathNs
	}
	return nsStr + parentPath, nil
}

func GetPathDepth(fullPath string) int {
	if fullPath == "" {
		return 0
	}
	pathDepth := strings.Count(fullPath, "/")
	if fullPath[len(fullPath)-1] == '/' {
		pathDepth = pathDepth - 1
	}
	return pathDepth
}
