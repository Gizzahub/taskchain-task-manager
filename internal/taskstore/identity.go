package taskstore

import "github.com/Gizzahub/taskchain-task-manager/internal/cardid"

func identityKey(raw string) string {
	id, err := cardid.Parse(raw)
	if err != nil {
		return ""
	}
	return id.Key()
}

func sameIdentity(a, b string) bool {
	key := identityKey(a)
	return key != "" && key == identityKey(b)
}

func isWorkTask(raw string) bool {
	id, err := cardid.Parse(raw)
	return err == nil && id.Prefix == "TASK"
}
