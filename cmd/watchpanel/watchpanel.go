package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

var VAR_RE = regexp.MustCompile("^([A-Za-z][A-Za-z0-9_]*)\\s*=\\s*([^\n]+)$")
var SUB_RE = regexp.MustCompile("\\$\\{([A-Za-z][A-Za-z0-9_]*)\\}")

func ProcessFile(fileName string, zoneName string, panelName string) error {
	log.Printf("process file: %s\n", fileName)
	file, err := os.Open(fileName)
	if err != nil {
		return err
	}
	defer file.Close()

	vars := make(map[string]string)
	panel := dash.DefinePanel(panelName)
	panel.TrackAnonControls(false)
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		mode := "vars" // vars or printf
		line = strings.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if line[0] == '#' {
			continue
		}
		if mode == "vars" {
			matchArr := VAR_RE.FindStringSubmatch(line)
			if matchArr != nil {
				vars[matchArr[1]] = matchArr[2]
				continue
			}
			if strings.HasPrefix(line, "---") {
				mode = "printf"
				continue
			}
		}
		mode = "printf"
		numReplace := 0
		for {
			matchArr := SUB_RE.FindStringSubmatch(line)
			if matchArr == nil {
				break
			}
			varName := matchArr[1]
			if vars[varName] == "" {
				fmt.Printf("WARNING variable %s used, but no definition, line:%d\n", varName, lineNo)
			}
			line = strings.Replace(line, fmt.Sprintf("${%s}", matchArr[1]), vars[varName], 1)
			numReplace++
			if numReplace > 100 {
				fmt.Printf("WARNING, too many replacements, line:%d\n", lineNo)
				break
			}
		}
		panel.Print(line)
	}
	if scanner.Err() != nil {
		return scanner.Err()
	}
	panel.Flush()
	panel.Dump(os.Stdout)
	if len(panel.ElemBuilder.Errs) > 0 {
		for _, err := range panel.ElemBuilder.Errs {
			fmt.Printf("%s\n", err.String())
		}
	}
	return err
}

func WatchPanel(fileName string, zoneName string, panelName string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	ProcessFile(fileName, zoneName, panelName)
	watcher.Add(fileName)
	for {
		select {
		case event := <-watcher.Events:
			if event.Op == fsnotify.Create || event.Op == fsnotify.Write {
				err = ProcessFile(fileName, zoneName, panelName)
				if err != nil {
					log.Printf("ERROR processing file %s, err:%v\n", fileName, err)
				}
			}

		case err = <-watcher.Errors:
			if err != nil {
				log.Printf("Watcher got err:%v\n", err)
			}
		}
	}
	return nil
}

func main() {
	zoneName := flag.String("zone", "default", "Dashborg ZoneName")
	panelName := flag.String("panel", "default", "Dashbog PanelName")
	keyName := flag.String("key", dash.TLS_KEY_FILENAME, "Account Key File")
	crtName := flag.String("crt", dash.TLS_CERT_FILENAME, "Account Certificate File")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Printf("Usage: watchpanel [filename]\n")
		flag.PrintDefaults()
		return
	}
	fileName := flag.Arg(0)
	log.Printf("Watching %s for %s/%s\n", fileName, *zoneName, *panelName)

	cfg := &dash.Config{ProcName: "watch", ZoneName: *zoneName, AnonAcc: true, Env: "dev"}
	cfg.UseKeys(*keyName, *crtName, true)
	defer dash.StartProcClient(cfg).WaitForClear()
	WatchPanel(fileName, *zoneName, *panelName)
}
