package datasetinstall

import "time"

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

type Source struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	Repository    string `json:"repository"`
	Revision      string `json:"revision"`
	License       string `json:"license"`
	DownloadSize  string `json:"download_size"`
	DataRoot      string `json:"data_root"`
	Downloaded    bool   `json:"downloaded"`
	Prepared      bool   `json:"prepared"`
	InstallStatus string `json:"install_status"`
	Note          string `json:"note,omitempty"`
}

type Event struct {
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type Task struct {
	ID                    string     `json:"id"`
	Dataset               string     `json:"dataset"`
	Status                string     `json:"status"`
	DataRoot              string     `json:"data_root"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	StartedAt             *time.Time `json:"started_at,omitempty"`
	CompletedAt           *time.Time `json:"completed_at,omitempty"`
	CancellationRequested bool       `json:"cancellation_requested,omitempty"`
	Error                 string     `json:"error,omitempty"`
	Events                []Event    `json:"events"`
	IdempotencyKey        string     `json:"idempotency_key"`
}

type recipe struct {
	Source
	RawFiles []string
	Manifest string
}

func recipes() []recipe {
	return []recipe{
		{
			Source: Source{
				ID: "locomo", Name: "LoCoMo", Provider: "github",
				Repository: "snap-research/locomo",
				Revision:   "3eb6f2c585f5e1699204e3c3bdf7adc5c28cb376",
				License:    "CC-BY-NC-4.0", DownloadSize: "约 2.7 MB",
			},
			RawFiles: []string{"locomo/locomo10.json"},
			Manifest: "locomo.json",
		},
		{
			Source: Source{
				ID: "longmemeval", Name: "LongMemEval-S Cleaned", Provider: "huggingface",
				Repository: "xiaowu0162/longmemeval-cleaned",
				Revision:   "98d7416c24c778c2fee6e6f3006e7a073259d48f",
				License:    "MIT", DownloadSize: "约 265 MB",
			},
			RawFiles: []string{"longmemeval/longmemeval_s_cleaned.json"},
			Manifest: "longmemeval.json",
		},
		{
			Source: Source{
				ID: "longmemeval-v2", Name: "LongMemEval-V2 Small", Provider: "huggingface",
				Repository: "xiaowu0162/longmemeval-v2",
				Revision:   "f152293e235517d504809563c833d7190b8c713b",
				License:    "Apache-2.0", DownloadSize: "约 1.1 GB（不含轨迹截图）",
				Note: "下载和 prepare 可用；实验仍需 agent trajectory adapter。",
			},
			RawFiles: []string{
				"longmemeval-v2/checksums.sha256",
				"longmemeval-v2/questions.jsonl",
				"longmemeval-v2/trajectories.jsonl",
				"longmemeval-v2/haystacks/lme_v2_small.json",
				"longmemeval-v2/haystacks/lme_v2_medium.json",
			},
			Manifest: "longmemeval-v2.json",
		},
	}
}
