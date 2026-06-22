package model

import "testing"

func TestNotificationBatchTableNamesTableDriven(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "batch", got: (NotificationBatch{}).TableName(), want: "notification_batches"},
		{name: "item", got: (NotificationBatchItem{}).TableName(), want: "notification_batch_items"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("TableName = %q, want %q", tt.got, tt.want)
			}
		})
	}
}
