package dtmsvr

import (
	"fmt"
	"strings"

	"github.com/yedf/dtm/common"
)

type TransSagaProcessor struct {
	*TransGlobal
}

func init() {
	registorProcessorCreator("saga", func(trans *TransGlobal) TransProcessor { return &TransSagaProcessor{TransGlobal: trans} })
}

func (t *TransSagaProcessor) GenBranches() []TransBranch {
	branches := []TransBranch{}
	steps := []M{}
	common.MustUnmarshalString(t.Data, &steps)
	for _, step := range steps {
		branch := GenGid()
		for _, branchType := range []string{"compensate", "action"} {
			branches = append(branches, TransBranch{
				Gid:        t.Gid,
				BranchID:   branch,
				Data:       step["data"].(string),
				Url:        step[branchType].(string),
				BranchType: branchType,
				Status:     "prepared",
			})
		}
	}
	return branches
}

func (t *TransSagaProcessor) ExecBranch(db *common.DB, branch *TransBranch) {
	resp, err := common.RestyClient.R().SetBody(branch.Data).SetQueryParams(t.getBranchParams(branch)).Post(branch.Url)
	e2p(err)
	body := resp.String()
	if strings.Contains(body, "SUCCESS") {
		t.touch(db, config.TransCronInterval)
		branch.changeStatus(db, "succeed")
	} else if branch.BranchType == "action" && strings.Contains(body, "FAIL") {
		t.touch(db, config.TransCronInterval)
		branch.changeStatus(db, "failed")
	} else {
		panic(fmt.Errorf("unknown response: %s, will be retried", body))
	}
}

func (t *TransSagaProcessor) ProcessOnce(db *common.DB, branches []TransBranch) {
	if t.Status != "submitted" {
		return
	}
	current := 0 // 当前正在处理的步骤
	for ; current < len(branches); current++ {
		branch := &branches[current]
		if branch.BranchType != "action" || branch.Status != "prepared" {
			continue
		}
		t.ExecBranch(db, branch)
		if branch.Status != "succeed" {
			break
		}
	}
	if current == len(branches) { // saga 事务完成
		t.changeStatus(db, "succeed")
		return
	}
	for current = current - 1; current >= 0; current-- {
		branch := &branches[current]
		if branch.BranchType != "compensate" || branch.Status != "prepared" {
			continue
		}
		t.ExecBranch(db, branch)
	}
	if current != -1 {
		panic(fmt.Errorf("saga current not -1"))
	}
	t.changeStatus(db.Must(), "failed")
}
