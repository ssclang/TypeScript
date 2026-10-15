#include <stdlib.h>
#include <node_api.h>
#include "_cgo_export.h"

static napi_value open(napi_env env, napi_callback_info info) {
	size_t argc = 1;
	napi_value argv[1];
	napi_get_cb_info(env, info, &argc, argv, NULL, NULL);
	char cwd[4096];
	size_t cwdLength;
	napi_get_value_string_utf8(env, argv[0], cwd, sizeof cwd, &cwdLength);
	sscApiOpen(cwd, (int)cwdLength);
	return NULL;
}

static napi_value request(napi_env env, napi_callback_info info) {
	size_t argc = 2;
	napi_value argv[2];
	napi_get_cb_info(env, info, &argc, argv, NULL, NULL);
	char method[256];
	size_t methodLength;
	napi_get_value_string_utf8(env, argv[0], method, sizeof method, &methodLength);
	void *params;
	size_t paramsLength;
	napi_get_buffer_info(env, argv[1], &params, &paramsLength);
	int resultLength = 0;
	int isError = 0;
	char *result = sscApiRequest(method, (int)methodLength, params, (int)paramsLength, &resultLength, &isError);
	napi_value value = NULL;
	if (isError) {
		result = realloc(result, resultLength + 1);
		result[resultLength] = 0;
		napi_throw_error(env, NULL, result);
	} else {
		napi_create_buffer_copy(env, resultLength, result, NULL, &value);
	}
	free(result);
	return value;
}

NAPI_MODULE_INIT() {
	napi_value function;
	napi_create_function(env, "open", NAPI_AUTO_LENGTH, open, NULL, &function);
	napi_set_named_property(env, exports, "open", function);
	napi_create_function(env, "request", NAPI_AUTO_LENGTH, request, NULL, &function);
	napi_set_named_property(env, exports, "request", function);
	return exports;
}
