package modules

import (
	_ "github.com/kuetix/std-http/modules/api/auth/transitions"
	_ "github.com/kuetix/std-http/modules/api/http/transitions"

	di "github.com/kuetix/container"
)

func init() {
	di.Boot()
}

func Enable() {}
