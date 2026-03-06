package modules

import (
	_ "http/modules/api/auth/transitions"
	_ "http/modules/api/http/transitions"

	di "github.com/kuetix/container"
)

func init() {
	di.Boot()
}

func Enable() {}
