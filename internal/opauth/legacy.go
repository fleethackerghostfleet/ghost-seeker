package opauth

import (
	"fmt"
	"os"
	"strconv"
)

func CheckLegacyOperatorAuth() error {
	value := os.Getenv("GS_REQUIRE_OPERATOR_AUTH")
	if value == "" {
		return nil
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil || enabled {
		return fmt.Errorf("GS_REQUIRE_OPERATOR_AUTH is no longer supported; configure token/PIV authentication explicitly and remove this variable")
	}
	return nil
}
