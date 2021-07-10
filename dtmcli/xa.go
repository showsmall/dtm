package dtmcli

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yedf/dtm/common"
)

type M = map[string]interface{}

var e2p = common.E2P

type XaGlobalFunc func(gid string) error

type XaLocalFunc func(db *common.DB) error

type Xa struct {
	Server      string
	Conf        map[string]string
	CallbackUrl string
}

func NewXa(server string, mysqlConf map[string]string, app *gin.Engine, callbackUrl string) *Xa {
	xa := &Xa{
		Server:      server,
		Conf:        mysqlConf,
		CallbackUrl: callbackUrl,
	}
	u, err := url.Parse(callbackUrl)
	e2p(err)
	app.POST(u.Path, common.WrapHandler(func(c *gin.Context) (interface{}, error) {
		type CallbackReq struct {
			Gid      string `json:"gid"`
			BranchID string `json:"branch_id"`
			Action   string `json:"action"`
		}
		req := CallbackReq{}
		b, err := c.GetRawData()
		e2p(err)
		common.MustUnmarshal(b, &req)
		tx, my := common.DbAlone(xa.Conf)
		defer my.Close()
		if req.Action == "commit" {
			tx.Must().Exec(fmt.Sprintf("xa commit '%s'", req.BranchID))
		} else if req.Action == "rollback" {
			tx.Must().Exec(fmt.Sprintf("xa rollback '%s'", req.BranchID))
		} else {
			panic(fmt.Errorf("unknown action: %s", req.Action))
		}
		return M{"result": "SUCCESS"}, nil
	}))
	return xa
}

func (xa *Xa) XaLocalTransaction(gid string, transFunc XaLocalFunc) (rerr error) {
	defer common.P2E(&rerr)
	branchID := GenGid(xa.Server)
	tx, my := common.DbAlone(xa.Conf)
	defer func() { my.Close() }()
	tx.Must().Exec(fmt.Sprintf("XA start '%s'", branchID))
	err := transFunc(tx)
	e2p(err)
	resp, err := common.RestyClient.R().
		SetBody(&M{"gid": gid, "branch_id": branchID, "trans_type": "xa", "status": "prepared", "url": xa.CallbackUrl}).
		Post(xa.Server + "/registerXaBranch")
	e2p(err)
	if !strings.Contains(resp.String(), "SUCCESS") {
		e2p(fmt.Errorf("unknown server response: %s", resp.String()))
	}
	tx.Must().Exec(fmt.Sprintf("XA end '%s'", branchID))
	tx.Must().Exec(fmt.Sprintf("XA prepare '%s'", branchID))
	return nil
}

func (xa *Xa) XaGlobalTransaction(transFunc XaGlobalFunc) (gid string, rerr error) {
	gid = GenGid(xa.Server)
	data := &M{
		"gid":        gid,
		"trans_type": "xa",
	}
	defer func() {
		x := recover()
		if x != nil {
			_, _ = common.RestyClient.R().SetBody(data).Post(xa.Server + "/abort")
			rerr = x.(error)
		}
	}()
	resp, rerr := common.RestyClient.R().SetBody(data).Post(xa.Server + "/prepare")
	e2p(rerr)
	if !strings.Contains(resp.String(), "SUCCESS") {
		panic(fmt.Errorf("unexpected result: %s", resp.String()))
	}
	rerr = transFunc(gid)
	e2p(rerr)
	resp, rerr = common.RestyClient.R().SetBody(data).Post(xa.Server + "/submit")
	e2p(rerr)
	if !strings.Contains(resp.String(), "SUCCESS") {
		panic(fmt.Errorf("unexpected result: %s", resp.String()))
	}
	return
}
