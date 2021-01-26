package main

import (
	"fmt"

	"github.com/mitchellh/mapstructure"
	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

type TodoModel struct {
	NewTodo string `json:"newtodo"`
}

type TodoItem struct {
	Id   int
	Item string
	Done bool
}

func main() {
	cfg := &dash.Config{ProcName: "todo", AnonAcc: true, AutoKeygen: true}
	dash.StartProcClient(cfg)
	defer dash.WaitForClear()
	var TodoList []*TodoItem
	NextId := 1

	dash.RegisterPanelHandler("todo", "/", func(req *dash.PanelRequest) error {
		req.NoAuth()
		req.SetHtmlFromFile("cmd/todo/todo.html")
		req.SetData("$.todos", TodoList)
		return nil
	})

	dash.RegisterPanelHandler("todo", "/add-todo", func(req *dash.PanelRequest) error {
		var model TodoModel
		mapstructure.Decode(req.Model, &model)
		if model.NewTodo == "" {
			return nil
		}
		TodoList = append(TodoList, &TodoItem{Id: NextId, Item: model.NewTodo})
		NextId++
		req.SetData("$.model.newtodo", "")
		req.SetData("$.todos", TodoList)
		return nil
	})
	dash.RegisterPanelHandler("todo", "/mark-todo-done", func(req *dash.PanelRequest) error {
		todoIdFloat, ok := req.Data.(float64)
		if !ok {
			return nil
		}
		todoId := int(todoIdFloat)
		for _, todoItem := range TodoList {
			if todoItem.Id == todoId {
				todoItem.Done = true
			}
		}
		req.SetData("$.todos", TodoList)
		return nil
	})
	dash.RegisterPanelHandler("todo", "/remove-todo", func(req *dash.PanelRequest) error {
		fmt.Printf("mark done %v\n", req.Data)
		todoIdFloat, ok := req.Data.(float64)
		if !ok {
			return nil
		}
		todoId := int(todoIdFloat)
		newList := make([]*TodoItem, 0)
		for _, todoItem := range TodoList {
			if todoItem.Id == todoId {
				continue
			}
			newList = append(newList, todoItem)
		}
		TodoList = newList
		req.SetData("$.todos", TodoList)
		return nil
	})

	select {}
}
