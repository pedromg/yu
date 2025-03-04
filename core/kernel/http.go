package kernel

import (
	"github.com/gin-gonic/gin"
	. "github.com/yu-org/yu/common"
	. "github.com/yu-org/yu/core"
	"github.com/yu-org/yu/core/context"
	"github.com/yu-org/yu/core/types"
	"io"
	"io/ioutil"
	"net/http"
)

const PARAMS_KEY = "params"

func (m *Kernel) HandleHttp() {
	r := gin.Default()

	// GET request
	r.GET(ExecApiPath, func(c *gin.Context) {
		m.handleHttpExec(c)
	})
	r.GET(QryApiPath, func(c *gin.Context) {
		m.handleHttpQry(c)
	})

	// POST request
	r.POST(ExecApiPath, func(c *gin.Context) {
		m.handleHttpExec(c)
	})
	r.POST(QryApiPath, func(c *gin.Context) {
		m.handleHttpQry(c)
	})

	// PUT request
	r.PUT(ExecApiPath, func(c *gin.Context) {
		m.handleHttpExec(c)
	})
	r.PUT(QryApiPath, func(c *gin.Context) {
		m.handleHttpQry(c)
	})

	// DELETE request
	r.DELETE(ExecApiPath, func(c *gin.Context) {
		m.handleHttpExec(c)
	})
	r.DELETE(QryApiPath, func(c *gin.Context) {
		m.handleHttpQry(c)
	})

	r.Run(m.httpPort)
}

func (m *Kernel) handleHttpExec(c *gin.Context) {
	params, err := getHttpJsonParams(c)
	if err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	_, _, stxn, err := getExecInfoFromReq(c.Request, params)
	if err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	switch m.RunMode {
	case MasterWorker:
		//ip, name, err := m.findWorkerIpAndName(tripodName, callName, ExecCall)
		//if err != nil {
		//	c.String(
		//		http.StatusBadRequest,
		//		FindNoCallStr(tripodName, callName, err),
		//	)
		//	return
		//}
		//
		//fmap := make(map[string]*TxnsAndWorkerName)
		//fmap[ip] = &TxnsAndWorkerName{
		//	Txns:       FromArray(stxn),
		//	WorkerName: name,
		//}
		//err = m.forwardTxnsForCheck(fmap)
		//if err != nil {
		//	c.AbortWithError(http.StatusInternalServerError, err)
		//	return
		//}
		//
		//err = m.txPool.Insert(name, stxn)
		//if err != nil {
		//	c.AbortWithError(http.StatusInternalServerError, err)
		//	return
		//}
	case LocalNode:
		err = m.txPool.Insert(stxn)
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
	}

	err = m.pubUnpackedTxns(types.FromArray(stxn))
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
	}
}

func (m *Kernel) handleHttpQry(c *gin.Context) {
	params, err := getHttpJsonParams(c)
	if err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	qcall, err := getQryInfoFromReq(c.Request, params)
	if err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	switch m.RunMode {
	case LocalNode:
		pubkey, err := GetPubkey(c.Request)
		if err != nil {

			return
		}
		ctx, err := context.NewContext(pubkey.Address(), qcall.Params)
		if err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}

		respObj, err := m.land.Query(qcall, ctx)
		if err != nil {
			c.String(
				http.StatusBadRequest,
				FindNoCallStr(qcall.TripodName, qcall.QueryName, err),
			)
			return
		}
		c.JSON(http.StatusOK, respObj)
	}

}

func readPostBody(body io.ReadCloser) (string, error) {
	byt, err := ioutil.ReadAll(body)
	return string(byt), err
}
