package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/sawka/dashborg-go-sdk/pkg/dash"
	"github.com/sawka/dashborg-go-sdk/pkg/dashcloud"
	"github.com/sawka/dashborg-go-sdk/pkg/dashlocal"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

type TodoItem struct {
	Id       int
	TodoType string
	Item     string
	Done     bool
}

type ServerTodoModel struct {
	TodoList []*TodoItem
	NextId   int
}

type TodoPanelState struct {
	TodoType string `json:"todotype"`
	NewTodo  string `json:"newtodo"`
}

func (m *ServerTodoModel) RootHandler(req *dash.PanelRequest) error {
	req.SetHtmlFromFile("cmd/todo/todo.html")
	return nil
}

func (m *ServerTodoModel) AddTodo2(req *dash.PanelRequest, state *TodoPanelState) error {
	req.SetData("$.errors", nil)
	if state.NewTodo == "" {
		req.SetData("$.errors", "Please enter a Todo Item")
		return nil
	}
	if state.TodoType == "" {
		req.SetData("$.errors", "Please select a Todo Type")
		return nil
	}
	m.TodoList = append(m.TodoList, &TodoItem{Id: m.NextId, Item: state.NewTodo, TodoType: state.TodoType})
	m.NextId++
	req.InvalidateData("/get-todo-list")
	req.SetData("$state.newtodo", "")
	return nil
}

func (m *ServerTodoModel) MarkTodoDone2(req *dash.PanelRequest, state interface{}, todoId int) error {
	for _, todoItem := range m.TodoList {
		if todoItem.Id == todoId {
			todoItem.Done = true
		}
	}
	req.InvalidateData("/get-todo-list")
	return nil
}

func (m *ServerTodoModel) RemoveTodo2(req *dash.PanelRequest, state interface{}, todoId int) error {
	newList := make([]*TodoItem, 0)
	for _, todoItem := range m.TodoList {
		if todoItem.Id == todoId {
			continue
		}
		newList = append(newList, todoItem)
	}
	m.TodoList = newList
	req.InvalidateData("/get-todo-list")
	return nil
}

func (m *ServerTodoModel) GetTodoList2(req *dash.PanelRequest) (interface{}, error) {
	return m.TodoList, nil
}

func (m *ServerTodoModel) StartStream(req *dash.PanelRequest) error {
	req.StartStream(dash.StreamOpts{ControlPath: "$.streamcontrol"}, func(ctx context.Context, req *dash.PanelRequest) {
		for i := 1; i <= 100; i++ {
			req.SetData("$.counter", i)
			req.Flush()
			time.Sleep(1 * time.Second)
		}
	})
	return nil
}

func SetCounter(req *dash.PanelRequest) error {
	req.SetData("$.counter", 10)
	return nil
}

func main() {
	tm := &ServerTodoModel{NextId: 1}
	app := dash.MakeApp("todo")
	app.SetAuth(dash.AuthNone{})
	app.SetHtmlFromFile("cmd/todo/todo.html")
	app.AppHandlerEx("/mark-todo-done", tm.MarkTodoDone2)
	app.DataHandlerEx("/get-todo-list", tm.GetTodoList2)
	app.AppHandlerEx("/add-todo", tm.AddTodo2)
	app.AppHandlerEx("/remove-todo", tm.RemoveTodo2)
	app.AppHandlerEx("/start-stream", tm.StartStream)
	app.AppHandler("/set-counter", SetCounter)
	app.SetOnLoadHandler("/set-counter")

	fmt.Printf("app %s\n", dashutil.MarshalJsonNoError(app.AppConfig()))

	if os.Getenv("LOCAL") != "" {
		container, err := dashlocal.MakeContainer(&dashlocal.ContainerConfig{Env: "dev"})
		if err != nil {
			fmt.Printf("Error creating local container: %v\n", err)
			return
		}
		container.StartContainer()
		container.ConnectApp(app, nil)
	} else {
		cfg := &dash.Config{
			ProcName:   "todo",
			AnonAcc:    true,
			AutoKeygen: true,
			ZoneName:   "default",
		}
		container, err := dashcloud.StartClient(cfg)
		if err != nil {
			fmt.Printf("Error connecting to Dashborg Cloud Service: %v\n", err)
			return
		}
		container.ConnectApp(app, nil)

		rz, _ := dash.ReflectZone()
		fmt.Printf("zone: %#v\n", rz)
	}

	select {}

}
