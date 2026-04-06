package modules

import (
	_ "github.com/kuetix/std-http/modules/api/http/transitions"

	di "github.com/kuetix/container"
	StdCoreModule "github.com/kuetix/std-core/modules"
)

func init() {
	di.Boot()
}

func Enable() {
	StdCoreModule.Enable()
}
