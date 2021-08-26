package dashcloud

func MakeClient(config *Config) (*DashCloudClient, error) {
	config.setDefaultsAndLoadKeys()
	container := makeCloudClient(config)
	err := container.startClient()
	if err != nil {
		return nil, err
	}
	return container, nil
}

type ReflectZoneType struct {
	AccId    string                     `json:"accid"`
	ZoneName string                     `json:"zonename"`
	Procs    map[string]ReflectProcType `json:"procs"`
	Apps     map[string]ReflectAppType  `json:"apps"`
}

type ReflectAppType struct {
	AppName    string   `json:"appname"`
	ProcRunIds []string `json:"procrunids"`
}

type ReflectProcType struct {
	StartTs   int64             `json:"startts"`
	ProcName  string            `json:"procname"`
	ProcTags  map[string]string `json:"proctags"`
	ProcRunId string            `json:"procrunid"`
}
