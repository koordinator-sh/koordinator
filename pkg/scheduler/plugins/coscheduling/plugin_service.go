package coscheduling

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/services"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/util"
)

var _ services.APIServiceProvider = &Coscheduling{}

func (p *Coscheduling) RegisterEndpoints(group *gin.RouterGroup) {
	group.GET("/gang/:namespace/:name", func(c *gin.Context) {
		gangNamespace := c.Param("namespace")
		gangName := c.Param("name")
		gangId := util.GetId(gangNamespace, gangName)
		gangSummary, exist := p.pgMgr.GetGangSummary(gangId)
		if !exist {
			services.ResponseErrorMessage(c, http.StatusNotFound, "cannot find gang %s/%s", gangNamespace, gangName)
			return
		}
		c.JSON(http.StatusOK, gangSummary)
	})
}
