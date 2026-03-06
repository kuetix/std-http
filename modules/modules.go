package modules

import (
	_ "std-http/modules/api/auth/transitions"
	_ "std-http/modules/api/http/transitions"

	di "github.com/kuetix/container"
)

func init() {
	di.Boot()
}

func Enable() {}
