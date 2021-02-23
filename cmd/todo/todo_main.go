package main

import (
	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

type TodoItem struct {
	Id   int
	Item string
	Done bool
}

type ServerTodoModel struct {
	TodoList []*TodoItem
	NextId   int
}

type TodoPanelState struct {
	NewTodo string `json:"newtodo"`
}

func (m *ServerTodoModel) RootHandler(req *dash.PanelRequest) error {
	req.SetHtmlFromFile("cmd/todo/todo.html")
	return nil
}

func (m *ServerTodoModel) AddTodo(req *dash.PanelRequest, state *TodoPanelState) error {
	if state.NewTodo == "" {
		return nil
	}
	m.TodoList = append(m.TodoList, &TodoItem{Id: m.NextId, Item: state.NewTodo})
	m.NextId++
	req.InvalidateData("/get-todo-list")
	req.SetData("$.state.newtodo", "")
	return nil
}

func (m *ServerTodoModel) MarkTodoDone(req *dash.PanelRequest, state interface{}, todoId int) error {
	for _, todoItem := range m.TodoList {
		if todoItem.Id == todoId {
			todoItem.Done = true
		}
	}
	req.InvalidateData("/get-todo-list")
	return nil
}

func (m *ServerTodoModel) RemoveTodo(req *dash.PanelRequest, state interface{}, todoId int) error {
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

func (m *ServerTodoModel) GetTodoList(req *dash.PanelRequest) (interface{}, error) {
	return m.TodoList, nil
}

func main() {
	cfg := &dash.Config{ProcName: "todo", AnonAcc: true, AutoKeygen: true}
	dash.StartProcClient(cfg)
	defer dash.WaitForClear()
	tm := &ServerTodoModel{NextId: 1}
	dash.RegisterPanelHandlerEx("todo", "/", tm.RootHandler)
	dash.RegisterPanelHandlerEx("todo", "/add-todo", tm.AddTodo)
	dash.RegisterPanelHandlerEx("todo", "/mark-todo-done", tm.MarkTodoDone)
	dash.RegisterPanelHandlerEx("todo", "/remove-todo", tm.RemoveTodo)
	dash.RegisterDataHandlerEx("todo", "/get-todo-list", tm.GetTodoList)
	select {}
}
