package main

import (
	"fmt"
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

	IsNotPaid bool // just for transport :/
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

var EmailRe = regexp.MustCompile("^[a-zA-Z0-9.-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$")

type CreateAccountFormData struct {
	Name  string `json:"name" mapstructure:"name"`
	Email string `json:"email" mapstructure:"email"`
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

type PanelModel struct {
	SelectedAccId string
	CreateData    CreateAccountFormData `json:"create" mapstructure:"create"`
}

func main() {
	rand.Seed(time.Now().Unix())
	cfg := &dash.Config{ProcName: "demo2", AnonAcc: true, AutoKeygen: true}
	dash.StartProcClient(cfg)
	defer dash.WaitForClear()
	Model = MakeAccModel()
	dash.RegisterPanelHandler("demo2", "/", func(req *dash.PanelRequest) error {
		if !req.CheckAuth(dash.AuthPassword{"hello"}) {
			return nil
		}
		err := req.SetHtmlFromFile("cmd/demo2/demo2.html")
		if err != nil {
			return err
		}
		return nil
	})
	dash.RegisterPanelHandler("demo2", "/acc/upgrade", func(req *dash.PanelRequest) error {
		accId, ok := req.Data.(string)
		if !ok {
			return fmt.Errorf("No AccountId Selected")
		}
		Model.Upgrade(accId)
		req.InvalidateData("/accounts/.*")
		return nil
	})
	dash.RegisterPanelHandler("demo2", "/acc/downgrade", func(req *dash.PanelRequest) error {
		accId, ok := req.Data.(string)
		if !ok {
			return fmt.Errorf("No AccountId Selected")
		}
		Model.Downgrade(accId)
		req.InvalidateData("/accounts/.*")
		return nil
	})
	dash.RegisterPanelHandler("demo2", "/acc/refresh-accounts", func(req *dash.PanelRequest) error {
		req.SetData("$state.selaccid", nil)
		req.InvalidateData("/accounts/.*")
		return nil
	})
	dash.RegisterPanelHandler("demo2", "/acc/regen-acclist", func(req *dash.PanelRequest) error {
		Model.RegenAccounts()
		req.SetData("$state.selaccid", nil)
		req.InvalidateData("/accounts/.*")
		return nil
	})
	dash.RegisterPanelHandler("demo2", "/acc/remove", func(req *dash.PanelRequest) error {
		accId, ok := req.Data.(string)
		if !ok {
			return fmt.Errorf("No AccountId Selected")
		}
		Model.RemoveAcc(accId)
		req.InvalidateData("/accounts/.*")
		req.SetData("$state.selaccid", nil)
		return nil
	})
	dash.RegisterPanelHandler("demo2", "/acc/open-create-account-modal", func(req *dash.PanelRequest) error {
		req.SetData("$state.createAccountModal", true)
		return nil
	})
	dash.RegisterPanelHandler("demo2", "/acc/close-modal", func(req *dash.PanelRequest) error {
		req.SetData("$state.createAccountModal", false)
		return nil
	})
	dash.RegisterPanelHandler("demo2", "/acc/create-account", func(req *dash.PanelRequest) error {
		var panelModel PanelModel
		err := mapstructure.Decode(req.PanelState, &panelModel)
		if err != nil {
			return err
		}
		errors := panelModel.CreateData.Validate()
		if len(errors) > 0 {
			req.SetData("$state.create.errors", errors)
			return nil
		}
		req.SetData("$state.create.errors", nil)
		newAccId := Model.CreateAcc(panelModel.CreateData.Name, panelModel.CreateData.Email)
		req.SetData("$state.createAccountModal", false)
		req.SetData("$state.selaccid", newAccId)
		req.InvalidateData("/accounts/.*")
		return nil
	})
	dash.RegisterDataHandler("demo2", "/accounts/get", func(req *dash.PanelRequest) (interface{}, error) {
		accId, ok := req.Data.(string)
		if !ok {
			return nil, nil
		}
		acc := Model.AccById(accId)
		if acc == nil {
			return nil, nil
		}
		acc.IsNotPaid = !acc.IsPaid
		return acc, nil
	})
	dash.RegisterDataHandler("demo2", "/accounts/list", func(req *dash.PanelRequest) (interface{}, error) {
		accList := Model.CopyAccList()
		return accList, nil
	})
	quitCh := make(chan bool)
	<-quitCh
}
