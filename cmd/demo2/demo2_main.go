package main

import (
	"fmt"
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

var AccLock = &sync.Mutex{}
var AllAccs []*AccType

type AccType struct {
	AccId   string
	AccName string
	IsPaid  bool
	Email   string
}

func RandomWord(list []string) string {
	return list[rand.Int31n(int32(len(list)))]
}

func AccById(id string) *AccType {
	AccLock.Lock()
	defer AccLock.Unlock()
	for _, acc := range AllAccs {
		if acc.AccId == id {
			return acc
		}
	}
	return nil
}

func MakeRandomAcc() *AccType {
	return &AccType{
		AccId:   uuid.New().String(),
		AccName: strings.TrimSpace(RandomWord(ModWordStrs) + " " + RandomWord(FruitStrs) + " " + RandomWord(SuffixStrs)),
		IsPaid:  false,
		Email:   RandomWord(EmailStrs) + strconv.Itoa(int(rand.Int31n(70)+10)) + "@nomail.com",
	}
}

func ShowAccDetail(accId string, req *dash.PanelRequest) {
	panel, _ := dash.LookupPanel("demo2")
	logger := panel.LookupControl("log", "log")
	acc := AccById(accId)
	ctx := req.LookupContext("ctx-accdetail")
	defer ctx.Flush()
	if acc == nil {
		logger.LogText("Account not found :/")
		ctx.Print("<div>[@padding=5px @paddingleft=15px @col]")
		ctx.Print("[@paddingtop=10px] Account ${accId:%s} not found.", dash.Var("accId", accId))
		ctx.Print("<link>[@handler=/acc/clear-detail @block] Clear")
		ctx.Print("</div>")
		return
	}
	ctx.Print("<div>[@padding=5px @paddingleft=15px @col]")
	ctx.Print("[@h3] Account Detail")
	ctx.Print("[@paddingtop=5px] [@bold @width=120px] Acc ID | ${accId:%s}", dash.Var("accId", acc.AccId))
	ctx.Print("[@paddingtop=5px] [@bold @width=120px] Name | ${accName:%s}", dash.Var("accName", acc.AccName))
	ctx.Print("[@paddingtop=5px] [@bold @width=120px] Paid Acc | ${isPaid:%v}", dash.Var("isPaid", acc.IsPaid))
	ctx.Print("[@paddingtop=5px] [@bold @width=120px] Email | ${email:%s}", dash.Var("email", acc.Email))
	ctx.Print("<div>[@row @paddingtop=15px]")
	if acc.IsPaid {
		ctx.Print("<button>[@handler=/acc/downgrade] Downgrade", dash.Attr("data", acc.AccId))
	} else {
		ctx.Print("<button>[@handler=/acc/upgrade] Upgrade To Paid", dash.Attr("data", acc.AccId))
	}
	ctx.Print("<button>[@handler=/acc/remove] Remove Account", dash.Attr("data", acc.AccId))
	ctx.Print("</div>")
	ctx.Print("</div>")
}

func RenderCreateAccountModal(req *dash.PanelRequest, errs map[string]string) {
	modal := req.LookupContext("modal")
	modal.Print("<div>[@modaltitle='Create Account' @col]")

	modal.Print("<div>[@alignitems=center]")
	modal.Print("<inputtext>[@formfield=name @form=create-account @inputlabel=Name]")
	if errs["name"] != "" {
		modal.Print("[@uilabel @uipointing=left @uicolor=red @uibasic] ${nameerr:%s}", dash.Var("nameerr", errs["name"]))
	}
	modal.Print("</div>")

	modal.Print("<div>[@alignitems=center @margintop=10px]")
	modal.Print("<inputtext>[@formfield=email @form=create-account @inputlabel=Email]")
	if errs["email"] != "" {
		modal.Print("[@uilabel @uipointing=left @uicolor=red @uibasic] ${emailerr:%s}", dash.Var("emailerr", errs["email"]))
	}
	modal.Print("</div>")

	modal.Print("<div>[@jc=center @margintop=20px]")
	modal.Print("<button>[@handler=/acc/create-account @dataform=create-account] Create Account")
	modal.Print("</div>")
	modal.Print("</div>")
	modal.Flush()
}

var EmailRe = regexp.MustCompile("^[a-zA-Z0-9.-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$")

type CreateAccountFormData struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func Setup() {
	panel, _ := dash.LookupPanel("demo2")
	logger := panel.LookupControl("log", "log")
	panel.OnRequest("/acc/refresh-accounts-revertdetail", func(req *dash.PanelRequest) error {
		req.LookupContext("ctx-accdetail").Revert()
		req.TriggerRequest("/acc/refresh-accounts", nil)
		return nil
	})
	panel.OnRequest("/acc/refresh-accounts", func(req *dash.PanelRequest) error {
		AccLock.Lock()
		defer AccLock.Unlock()
		logger.LogText("Refresh Accounts")
		accList := req.LookupContext("ctx-acclist")
		accList.Print("<div>[@padding=5px @col]")
		accList.Print("[@h3] Accounts")
		accList.Print("<div>[@grow @overflowy=auto @col]")
		for _, acc := range AllAccs {
			accList.Print("<div>[@row @alignitems=center]")
			accList.Print("<link>[@marginleft=8px @size=16px @handler=/acc/select-account] ${name:%s}", dash.Attr("data", acc.AccId), dash.Var("name", acc.AccName))
			if acc.IsPaid {
				accList.Print("[@uilabel @uicolor=blue @uisize=tiny @inline @marginleft=5px] Paid")
			}
			accList.Print("</div>")
		}
		accList.Print("</div>")
		accList.Print("</div>")
		accList.Flush()
		return nil
	})
	panel.OnRequest("/acc/select-account", func(req *dash.PanelRequest) error {
		accId := req.Data.(string)
		logger.LogText("Select Account id:%s", accId)
		ShowAccDetail(accId, req)
		return nil
	})
	panel.OnRequest("/acc/clear-detail", func(req *dash.PanelRequest) error {
		ctx := req.LookupContext("ctx-accdetail")
		ctx.Revert()
		return nil
	})
	panel.OnRequest("/acc/upgrade", func(req *dash.PanelRequest) error {
		accId := req.Data.(string)
		acc := AccById(accId)
		if acc == nil {
			logger.LogText("Account not found id:%s", accId)
			return nil
		}
		logger.LogText("Upgrade Account id:%s", accId)
		acc.IsPaid = true
		ShowAccDetail(accId, req)
		req.TriggerRequest("/acc/refresh-accounts", nil)
		return nil
	})
	panel.OnRequest("/acc/downgrade", func(req *dash.PanelRequest) error {
		accId := req.Data.(string)
		acc := AccById(accId)
		if acc == nil {
			logger.LogText("Account not found id:%s", accId)
			return nil
		}
		logger.LogText("Downgrade Account id:%s", accId)
		acc.IsPaid = false
		ShowAccDetail(accId, req)
		req.TriggerRequest("/acc/refresh-accounts", nil)
		return nil
	})
	panel.OnRequest("/acc/remove", func(req *dash.PanelRequest) error {
		accId := req.Data.(string)
		AccLock.Lock()
		defer AccLock.Unlock()
		pos := -1
		for idx, acc := range AllAccs {
			if acc.AccId == accId {
				pos = idx
				break
			}
		}
		if pos == -1 {
			logger.LogText("Account not found id:%s", accId)
			return nil
		}
		AllAccs = append(AllAccs[:pos], AllAccs[pos+1:]...)
		req.TriggerRequest("/acc/refresh-accounts-revertdetail", nil)
		return nil
	})
	panel.OnRequest("/acc/regen-acclist", func(req *dash.PanelRequest) error {
		AccLock.Lock()
		defer AccLock.Unlock()
		AllAccs = make([]*AccType, 0)
		for i := 0; i < 5; i++ {
			AllAccs = append(AllAccs, MakeRandomAcc())
		}
		req.TriggerRequest("/acc/refresh-accounts-revertdetail", nil)
		return nil
	})
	panel.OnRequest("/acc/show-create-account", func(req *dash.PanelRequest) error {
		RenderCreateAccountModal(req, nil)
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
		errors := make(map[string]string)
		if formData.Name == "" {
			errors["name"] = "Name must not be empty"
		} else if len(formData.Name) > 40 {
			errors["name"] = "Name can only be 40 characters"
		}
		if formData.Email == "" {
			errors["email"] = "Email must not be empty"
		} else if len(formData.Email) > 40 {
			errors["email"] = "Email can only be 40 characters"
		} else if !EmailRe.MatchString(formData.Email) {
			errors["email"] = "Email format is not correct"
		}
		if len(errors) > 0 {
			RenderCreateAccountModal(req, errors)
			return nil
		}
		AccLock.Lock()
		defer AccLock.Unlock()
		accId := uuid.New().String()
		AllAccs = append(AllAccs, &AccType{AccId: accId, AccName: formData.Name, Email: formData.Email})
		logger.LogText("Account created id:%s", accId)
		req.TriggerRequest("/acc/refresh-accounts", nil)
		req.TriggerRequest("/acc/select-account", accId)
		modal := req.LookupContext("modal")
		modal.Revert()
		return nil
	})
}

func main() {
	cfg := &dash.Config{ProcName: "demo1", AnonAcc: true, Env: "dev"}
	cfg.UseAnonKeys()
	defer dash.StartProcClient(cfg).WaitForClear()

	rand.Seed(time.Now().Unix())

	for i := 0; i < 5; i++ {
		AllAccs = append(AllAccs, MakeRandomAcc())
	}

	quitCh := make(chan bool)
	demo2Panel, _ := dash.LookupPanel("demo2")
	stopButton := demo2Panel.LookupControl("button", "btn-stop")
	stopButton.OnClick(func() error {
		close(quitCh)
		return nil
	})
	Setup()
	<-quitCh
}
