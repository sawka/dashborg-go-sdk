package main

import (
	"fmt"
	"log"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mitchellh/mapstructure"
	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

var FruitStrs = []string{"Apple", "Banana", "Guava", "Orange", "Blackberry", "Mango", "Kiwi", "Raspberry", "Pineapple", "Avacado", "Onion", "Lettuce", "Cheese"}
var SuffixStrs = []string{"LLC", "Inc", "Corp", "Corp", "Ltd", "", "", "", "", "", "", "", ""}
var ModWordStrs = []string{"Star", "Lightning", "Flash", "Media", "Data", "Micro", "Net", "Echo", "World", "Red", "Blue", "Green", "Yellow", "Purple", "Tele", "Cloud", "Insta", "Face"}
var EmailStrs = []string{"mike", "matt", "michelle", "pat", "jim", "marc", "andrew", "alan"}

var Model *AccModel

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
		cw.Print("<div>[col @padding=5px @paddingleft=15px]")
		cw.Print("[@paddingtop=10px] Account ${accId:%s} not found.", dash.Var("accId", accId))
		cw.Print("<link/>[@trigger=ctx-accdetail @block] Clear")
		cw.Print("</div>")
		return nil
	}
	cw.Print("<div>[col]")
	cw.Print("*[row] [s2 @bold] Acc ID || ${accId:%s}", dash.Var("accId", acc.AccId))
	cw.Print("*[row] [s2 @bold] Name || ${accName:%s}", dash.Var("accName", acc.AccName))
	cw.Print("*[row] [s2 @bold] Paid Acc || ${isPaid:%v}", dash.Var("isPaid", acc.IsPaid))
	cw.Print("*[row] [s2 @bold] Email || ${email:%s}", dash.Var("email", acc.Email))
	cw.Print("<div>[row]")
	if acc.IsPaid {
		cw.Print("<button/>[@handler=/acc/downgrade] Downgrade", dash.JsonAttr("jsondata", acc.AccId))
	} else {
		cw.Print("<button/>[@handler=/acc/upgrade] Upgrade To Paid", dash.JsonAttr("jsondata", acc.AccId))
	}
	cw.Print("<button/>[@handler=/acc/remove] Remove Account", dash.JsonAttr("jsondata", acc.AccId))
	cw.Print("</div>")
	cw.Print("</div>")
	return nil
}

func RenderCreateAccountModal(cw *dash.ContextWriter, req *dash.PanelRequest) error {
	var errs map[string]string
	req.DecodeData(&errs)
	modal := req.LookupContext("modal")
	templateStr := `
      <div>[col @modaltitle='Create Account']
        <div>[col @alignitems=center @width=100%]
          <input:text/>[@noflex @formfield=name @form=create-account @inputlabel=Name @width=100%]
          {{if .name}}[@uilabel @uipointing=above @uicolor=red @uibasic @alignself=flex-start] {{.name}}{{end}}
        </div>
        <div>[col @alignitems=center @margintop=10px]
          <input:text/>[@noflex @formfield=email @form=create-account @inputlabel=Email @width=100%]
          {{if .email}}[@uilabel @uipointing=above @uicolor=red @uibasic @alignself=flex-start] {{.email}}{{end}}
        </div>
        <div>[@jc=center @margintop=20px]
          <button/>[@handler=/acc/create-account @dataform=create-account] Create Account
        </div>
      </div>`
	modal.PrintTemplate(templateStr, errs)
	modal.Flush()
	return nil
}

func RenderAccList(cw *dash.ContextWriter, req *dash.PanelRequest) error {
	logger := req.Panel.LookupControl("log", "log")
	Model.Lock.Lock()
	defer Model.Lock.Unlock()
	logger.LogText("Refresh Accounts (ctx-request)")
	cw.Print("<div>[col @grow @overflowy=auto]")
	for _, acc := range Model.Accs {
		cw.Print("<div>[row @alignitems=center]")
		cw.Print("<link/>[@marginleft=8px @size=16px @trigger=ctx-accdetail] ${name:%s}", dash.JsonAttr("jsondata", acc.AccId), dash.Var("name", acc.AccName))
		if acc.IsPaid {
			cw.Print("[@uilabel @uicolor=blue @uisize=tiny @inline @marginleft=5px] Paid")
		}
		cw.Print("</div>")
	}
	cw.Print("</div>")
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

	panel.LookupControl("context", "ctx-acclist").OnContextRequest(RenderAccList)
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
	Model = MakeAccModel()
	cfg := &dash.Config{ProcName: "demo2", AnonAcc: true, AutoKeygen: true}
	defer dash.StartProcClient(cfg).WaitForClear()

	demo2Panel, err := dash.DefinePanelFromFile("demo2", "cmd/demo2/demo2-panel.txt")
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
