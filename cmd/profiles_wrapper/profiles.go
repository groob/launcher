package main

import (
	"context"
	"fmt"

	"github.com/kolide/launcher/macos/profile"
)

func main() {
	ctx := context.TODO()
	err := profile.Remove(ctx, "com.example.screensaver", profile.AsUser("victor"))
	fmt.Println("removed: ", err)

	path := "/Users/victor/yvrprefs/screensaver.mobileconfig"
	err = profile.Install(ctx, path, profile.AsUser("victor"))
	fmt.Println("installed: ", err)
}
