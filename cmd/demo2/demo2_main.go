package main

import (
	"fmt"
	"log"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/mitchellh/mapstructure"
	"github.com/sawka/dashborg-go-sdk/pkg/dash"
	"github.com/sawka/dashborg-go-sdk/pkg/dashtmpl"
)

var FruitStrs = []string{"Apple", "Banana", "Guava", "Orange", "Blackberry", "Mango", "Kiwi", "Raspberry", "Pineapple", "Avacado", "Onion", "Lettuce", "Cheese"}
var SuffixStrs = []string{"LLC", "Inc", "Corp", "Corp", "Ltd", "", "", "", "", "", "", "", ""}
var ModWordStrs = []string{"Star", "Lightning", "Flash", "Media", "Data", "Micro", "Net", "Echo", "World", "Red", "Blue", "Green", "Yellow", "Purple", "Tele", "Cloud", "Insta", "Face"}
var EmailStrs = []string{"mike", "matt", "michelle", "pat", "jim", "marc", "andrew", "alan"}

var Model *AccModel
var DBTemplates *template.Template

type AccType struct {
	AccId   string
	AccName string
	IsPaid  bool
	Email   string
}

type AccModel struct {
	Lock *sync.Mutex
	Accs []*AccType
}

func MakeAccModel() *AccModel {
	rtn := &AccModel{Lock: &sync.Mutex{}}
	rtn.RegenAccounts()
	return rtn
}

func (m *AccModel) AccById(id string) *AccType {
	m.Lock.Lock()
	defer m.Lock.Unlock()
	return m.accByIdNoLock(id)
}

func (m *AccModel) CopyAccList() []*AccType {
	m.Lock.Lock()
	defer m.Lock.Unlock()
	rtn := make([]*AccType, len(m.Accs))
	for i := 0; i < len(m.Accs); i++ {
		accCopy := *m.Accs[i]
		rtn[i] = &accCopy
	}
	return rtn
}

func (m *AccModel) accByIdNoLock(id string) *AccType {
	for _, acc := range m.Accs {
		if acc.AccId == id {
			return acc
		}
	}
	return nil
}

func (m *AccModel) CreateAcc(name string, email string) string {
	m.Lock.Lock()
	defer m.Lock.Unlock()
	accId := uuid.New().String()
	m.Accs = append(m.Accs, &AccType{AccId: accId, AccName: name, Email: email})
	return accId
}

func (m *AccModel) RemoveAcc(id string) bool {
	m.Lock.Lock()
	defer m.Lock.Unlock()

	pos := -1
	for idx, acc := range m.Accs {
		if acc.AccId == id {
			pos = idx
			break
		}
	}
	if pos == -1 {
		return false
	}
	m.Accs = append(m.Accs[:pos], m.Accs[pos+1:]...)
	return true
}

func (m *AccModel) Upgrade(id string) bool {
	m.Lock.Lock()
	defer m.Lock.Unlock()
	acc := m.accByIdNoLock(id)
	if acc == nil {
		return false
	}
	acc.IsPaid = true
	return true
}

func (m *AccModel) Downgrade(id string) bool {
	m.Lock.Lock()
	defer m.Lock.Unlock()
	acc := m.accByIdNoLock(id)
	if acc == nil {
		return false
	}
	acc.IsPaid = false
	return true
}

func (m *AccModel) RegenAccounts() {
	m.Lock.Lock()
	defer m.Lock.Unlock()
	m.Accs = nil
	for i := 0; i < 5; i++ {
		m.Accs = append(m.Accs, MakeRandomAcc())
	}
}

func RandomWord(list []string) string {
	return list[rand.Int31n(int32(len(list)))]
}

func MakeRandomAcc() *AccType {
	return &AccType{
		AccId:   uuid.New().String(),
		AccName: strings.TrimSpace(RandomWord(ModWordStrs) + " " + RandomWord(FruitStrs) + " " + RandomWord(SuffixStrs)),
		IsPaid:  false,
		Email:   RandomWord(EmailStrs) + strconv.Itoa(int(rand.Int31n(70)+10)) + "@nomail.com",
	}
}

func RenderAccDetail(cw *dash.ContextWriter, req *dash.PanelRequest) error {
	logger := req.Panel.LookupControl("log", "log")
	accId, ok := req.Data.(string)
	logger.LogText("ShowAccDetail accId:%s", accId)
	if !ok {
		cw.Revert()
		return nil
	}
	defer cw.Flush()
	acc := Model.AccById(accId)
	if acc == nil {
		return dashtmpl.PrintTemplate(cw, DBTemplates, "ctx-accdetail/no-account", map[string]string{"AccId": accId})
	}
	return dashtmpl.PrintTemplate(cw, DBTemplates, "ctx-accdetail", acc)
}

func RenderCreateAccountModal(cw *dash.ContextWriter, req *dash.PanelRequest) error {
	var errs map[string]string
	req.DecodeData(&errs)
	modal := req.LookupContext("modal")
	err := dashtmpl.PrintTemplate(modal, DBTemplates, "modal", errs)
	if err != nil {
		return err
	}
	modal.Flush()
	return nil
}

func AccListData(req *dash.PanelRequest) (interface{}, error) {
	logger := req.Panel.LookupControl("log", "log")
	logger.LogText("Refresh Accounts (ctx-request)")
	return Model.CopyAccList(), nil
}

func RenderAccList(cw *dash.ContextWriter, req *dash.PanelRequest) error {
	logger := req.Panel.LookupControl("log", "log")
	Model.Lock.Lock()
	defer Model.Lock.Unlock()
	logger.LogText("Refresh Accounts (ctx-request)")
	err := dashtmpl.PrintTemplate(cw, DBTemplates, "ctx-acclist", Model.Accs)
	if err != nil {
		return err
	}
	cw.Flush()
	return nil
}

var EmailRe = regexp.MustCompile("^[a-zA-Z0-9.-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$")

type CreateAccountFormData struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (d CreateAccountFormData) Validate() map[string]string {
	errors := make(map[string]string)
	if d.Name == "" {
		errors["name"] = "Name must not be empty"
	} else if len(d.Name) > 40 {
		errors["name"] = "Name can only be 40 characters"
	}
	if d.Email == "" {
		errors["email"] = "Email must not be empty"
	} else if len(d.Email) > 40 {
		errors["email"] = "Email can only be 40 characters"
	} else if !EmailRe.MatchString(d.Email) {
		errors["email"] = "Email format is not correct"
	}
	return errors
}

func Setup() {
	panel, _ := dash.LookupPanel("demo2")
	logger := panel.LookupControl("log", "log")
	logger.LogText("Starting Demo2")

	// panel.LookupControl("context", "ctx-acclist").OnContextRequest(RenderAccList)
	dashtmpl.RenderContextTemplate(panel, "ctx-acclist", AccListData, DBTemplates)
	panel.LookupControl("context", "ctx-accdetail").OnContextRequest(RenderAccDetail)
	panel.LookupControl("context", "modal").OnContextRequest(RenderCreateAccountModal)

	panel.OnRequest("/acc/upgrade", func(req *dash.PanelRequest) error {
		accId := req.Data.(string)
		ok := Model.Upgrade(accId)
		if !ok {
			logger.LogText("Account not found id:%s", accId)
			return nil
		}
		logger.LogText("Upgrade Account id:%s", accId)
		req.TriggerContext("ctx-acclist", nil)
		req.TriggerContext("ctx-accdetail", accId)
		return nil
	})
	panel.OnRequest("/acc/downgrade", func(req *dash.PanelRequest) error {
		accId := req.Data.(string)
		ok := Model.Downgrade(accId)
		if !ok {
			logger.LogText("Account not found id:%s", accId)
			return nil
		}
		logger.LogText("Downgrade Account id:%s", accId)
		req.TriggerContext("ctx-acclist", nil)
		req.TriggerContext("ctx-accdetail", accId)
		return nil
	})
	panel.OnRequest("/acc/remove", func(req *dash.PanelRequest) error {
		accId := req.Data.(string)
		ok := Model.RemoveAcc(accId)
		if !ok {
			logger.LogText("Account not found id:%s", accId)
			return nil
		}
		req.TriggerContext("ctx-acclist", nil)
		req.TriggerContext("ctx-accdetail", nil)
		return nil
	})
	panel.OnRequest("/acc/regen-acclist", func(req *dash.PanelRequest) error {
		logger.LogText("Regen Account List")
		Model.RegenAccounts()
		req.TriggerContext("ctx-acclist", nil)
		req.TriggerContext("ctx-accdetail", nil)
		return nil
	})
	panel.OnRequest("/acc/create-account", func(req *dash.PanelRequest) error {
		logger.LogText(fmt.Sprintf("Create Account, data:%#v", req.Data))
		var formData CreateAccountFormData
		err := mapstructure.Decode(req.Data, &formData)
		if err != nil {
			logger.LogText(fmt.Sprintf("Error decoding form data %v", err))
			return nil
		}
		errors := formData.Validate()
		if len(errors) > 0 {
			req.TriggerContext("modal", errors)
			return nil
		}
		accId := Model.CreateAcc(formData.Name, formData.Email)
		logger.LogText("Account created id:%s", accId)
		req.TriggerContext("ctx-acclist", nil)
		req.TriggerContext("ctx-accdetail", accId)
		req.LookupContext("modal").Revert()
		return nil
	})
}

func main() {
	rand.Seed(time.Now().Unix())
	cfg := &dash.Config{ProcName: "demo2", AnonAcc: true, AutoKeygen: true}
	defer dash.StartProcClient(cfg).WaitForClear()

	Model = MakeAccModel()
	DBTemplates = dashtmpl.NewTemplate("demo2-panel")
	_, err := DBTemplates.ParseFiles("cmd/demo2/demo2-panel.txt")
	if err != nil {
		fmt.Printf("Error parsing template demo2-panel.txt, err:%v\n", err)
		return
	}
	demo2Panel, err := dashtmpl.DefinePanelFromTemplate("demo2", DBTemplates, "panel", nil)
	if err != nil {
		log.Printf("ERROR Defining Panel: %v\n", err)
		return
	}
	quitCh := make(chan bool)
	stopButton := demo2Panel.LookupControl("button", "btn-stop")
	stopButton.OnClick(func() error {
		logger := demo2Panel.LookupControl("log", "log")
		logger.LogText("Stop Button Clicked")
		close(quitCh)
		return nil
	})
	Setup()
	<-quitCh
}
