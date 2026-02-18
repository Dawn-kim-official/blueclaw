package memory

/*
#cgo CFLAGS: -DSQLITE_CORE -Wno-deprecated-declarations
#cgo linux LDFLAGS: -lm
#include "sqlite-vec.h"

static void register_vec_auto_extension() {
	sqlite3_auto_extension((void(*)(void))sqlite3_vec_init);
}
*/
import "C"

func registerVecExtension() {
	C.register_vec_auto_extension()
}
