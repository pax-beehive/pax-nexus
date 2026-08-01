package experimenttask

type ModelOption struct {
	ID       string
	Name     string
	Provider string
}

func SupportedModels() []ModelOption {
	return []ModelOption{
		{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", Provider: "deepseek"},
		{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", Provider: "deepseek"},
	}
}
