package http

import (
	"fmt"
	"testing"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.k6.io/k6/metrics"
)

func TestBatch(t *testing.T) {
	t.Parallel()
	tb, state, samples, rt, _ := newRuntime(t)
	sr := tb.Replacer.Replace
	t.Run("error", func(t *testing.T) {
		invalidURLerr := `invalid URL: parse "https:// invalidurl.com": invalid character " " in host name`
		testCases := []struct {
			name, code, expErr string
			throw              bool
		}{
			{
				name: "no arg", code: ``,
				expErr: `no argument was provided to http.batch()`, throw: true,
			},
			{
				name: "invalid null arg", code: `null`,
				expErr: `invalid http.batch() argument type <nil>`, throw: true,
			},
			{
				name: "invalid undefined arg", code: `undefined`,
				expErr: `invalid http.batch() argument type <nil>`, throw: true,
			},
			{
				name: "invalid string arg", code: `"https://somevalidurl.com"`,
				expErr: `invalid http.batch() argument type string`, throw: true,
			},
			{
				name: "invalid URL short", code: `["https:// invalidurl.com"]`,
				expErr: invalidURLerr, throw: true,
			},
			{
				name: "invalid URL short no throw", code: `["https:// invalidurl.com"]`,
				expErr: invalidURLerr, throw: false,
			},
			{
				name: "invalid URL array", code: `[ ["GET", "https:// invalidurl.com"] ]`,
				expErr: invalidURLerr, throw: true,
			},
			{
				name: "invalid URL array no throw", code: `[ ["GET", "https:// invalidurl.com"] ]`,
				expErr: invalidURLerr, throw: false,
			},
			{
				name: "invalid URL object", code: `[ {method: "GET", url: "https:// invalidurl.com"} ]`,
				expErr: invalidURLerr, throw: true,
			},
			{
				name: "invalid object no throw", code: `[ {method: "GET", url: "https:// invalidurl.com"} ]`,
				expErr: invalidURLerr, throw: false,
			},
			{
				name: "object no url key", code: `[ {method: "GET"} ]`,
				expErr: `batch request 0 doesn't have a url key`, throw: true,
			},
			{
				name: "multiple arguments", code: `["GET", "https://test.k6.io"],["GET", "https://test.k6.io"]`,
				expErr: `http.batch() accepts only an array or an object of requests`, throw: true,
			},
		}

		for _, tc := range testCases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) { //nolint:paralleltest
				oldThrow := state.Options.Throw.Bool
				state.Options.Throw.Bool = tc.throw
				defer func() { state.Options.Throw.Bool = oldThrow }()

				hook := logtest.NewLocal(state.Logger)
				defer hook.Reset()

				ret, err := rt.RunString(fmt.Sprintf(`
						(function(){
							var r = http.batch(%s);
							if (r.length !== 1) throw new Error('unexpected responses length: '+r.length);
							return {error: r[0].error, error_code: r[0].error_code};
						})()`, tc.code))
				if tc.throw {
					require.Error(t, err)
					assert.Contains(t, err.Error(), tc.expErr)
					require.Nil(t, ret)
				} else {
					require.NoError(t, err)
					require.NotNil(t, ret)
					var retobj map[string]interface{}
					var ok bool
					if retobj, ok = ret.Export().(map[string]interface{}); !ok {
						require.Fail(t, "got wrong return object: %#+v", retobj)
					}
					require.Equal(t, int64(1020), retobj["error_code"])
					require.Equal(t, invalidURLerr, retobj["error"])

					logEntry := hook.LastEntry()
					require.NotNil(t, logEntry)
					assert.Equal(t, logrus.WarnLevel, logEntry.Level)
					assert.Contains(t, logEntry.Data["error"].(error).Error(), tc.expErr)
					assert.Equal(t, "A batch request failed", logEntry.Message)
				}
			})
		}
	})
	t.Run("error,nopanic", func(t *testing.T) { //nolint:paralleltest
		invalidURLerr := `invalid URL: parse "https:// invalidurl.com": invalid character " " in host name`
		testCases := []struct{ name, code string }{
			{
				name: "array", code: `[
						["GET", "https:// invalidurl.com"],
						["GET", "https://somevalidurl.com"],
					]`,
			},
			{
				name: "object", code: `[
						{method: "GET", url: "https:// invalidurl.com"},
						{method: "GET", url: "https://somevalidurl.com"},
					]`,
			},
		}

		for _, tc := range testCases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) { //nolint:paralleltest
				oldThrow := state.Options.Throw.Bool
				state.Options.Throw.Bool = false
				defer func() { state.Options.Throw.Bool = oldThrow }()

				hook := logtest.NewLocal(state.Logger)
				defer hook.Reset()

				ret, err := rt.RunString(fmt.Sprintf(`
						(function(){
							var r = http.batch(%s);
							if (r.length !== 2) throw new Error('unexpected responses length: '+r.length);
							if (r[1] !== null) throw new Error('expected response at index 1 to be null');
							r[0].html();
							r[0].json();
	            		    return r[0].error_code; // not reached because of json()
						})()
					`, tc.code))
				require.Error(t, err)
				assert.Nil(t, ret)
				assert.Contains(t, err.Error(), "unexpected end of JSON input")
				logEntry := hook.LastEntry()
				require.NotNil(t, logEntry)
				assert.Equal(t, logrus.WarnLevel, logEntry.Level)
				assert.Contains(t, logEntry.Data["error"].(error).Error(), invalidURLerr)
				assert.Equal(t, "A batch request failed", logEntry.Message)
			})
		}
	})
	t.Run("GET", func(t *testing.T) {
		_, err := rt.RunString(sr(`
		{
			let reqs = [
				["GET", "HTTPBIN_URL/"],
				["GET", "HTTPBIN_IP_URL/"],
			];
			let res = http.batch(reqs);
			for (var key in res) {
				if (res[key].status != 200) { throw new Error("wrong status: " + res[key].status); }
				if (res[key].url != reqs[key][1]) { throw new Error("wrong url: " + res[key].url); }
			}
		}`))
		require.NoError(t, err)
		bufSamples := metrics.GetBufferedSamples(samples)
		assertRequestMetricsEmitted(t, bufSamples, "GET", sr("HTTPBIN_URL/"), 200, "")
		assertRequestMetricsEmitted(t, bufSamples, "GET", sr("HTTPBIN_IP_URL/"), 200, "")

		t.Run("Tagged", func(t *testing.T) {
			_, err := rt.RunString(sr(`
			{
				let fragment = "get";
				let reqs = [
					["GET", http.url` + "`" + `HTTPBIN_URL/${fragment}` + "`" + `],
					["GET", http.url` + "`" + `HTTPBIN_IP_URL/` + "`" + `],
				];
				let res = http.batch(reqs);
				for (var key in res) {
					if (res[key].status != 200) { throw new Error("wrong status: " + key + ": " + res[key].status); }
					if (res[key].url != reqs[key][1].url) { throw new Error("wrong url: " + key + ": " + res[key].url + " != " + reqs[key][1].url); }
				}
			}`))
			assert.NoError(t, err)
			bufSamples := metrics.GetBufferedSamples(samples)
			assertRequestMetricsEmitted(t, bufSamples, "GET", sr("HTTPBIN_URL/${}"), 200, "")
			assertRequestMetricsEmitted(t, bufSamples, "GET", sr("HTTPBIN_IP_URL/"), 200, "")
		})

		t.Run("Shorthand", func(t *testing.T) {
			_, err := rt.RunString(sr(`
			{
				let reqs = [
					"HTTPBIN_URL/",
					"HTTPBIN_IP_URL/",
				];
				let res = http.batch(reqs);
				for (var key in res) {
					if (res[key].status != 200) { throw new Error("wrong status: " + key + ": " + res[key].status); }
					if (res[key].url != reqs[key]) { throw new Error("wrong url: " + key + ": " + res[key].url); }
				}
			}`))
			assert.NoError(t, err)
			bufSamples := metrics.GetBufferedSamples(samples)
			assertRequestMetricsEmitted(t, bufSamples, "GET", sr("HTTPBIN_URL/"), 200, "")
			assertRequestMetricsEmitted(t, bufSamples, "GET", sr("HTTPBIN_IP_URL/"), 200, "")

			t.Run("Tagged", func(t *testing.T) {
				_, err := rt.RunString(sr(`
				{
					let fragment = "get";
					let reqs = [
						http.url` + "`" + `HTTPBIN_URL/${fragment}` + "`" + `,
						http.url` + "`" + `HTTPBIN_IP_URL/` + "`" + `,
					];
					let res = http.batch(reqs);
					for (var key in res) {
						if (res[key].status != 200) { throw new Error("wrong status: " + key + ": " + res[key].status); }
						if (res[key].url != reqs[key].url) { throw new Error("wrong url: " + key + ": " + res[key].url + " != " + reqs[key].url); }
					}
				}`))
				assert.NoError(t, err)
				bufSamples := metrics.GetBufferedSamples(samples)
				assertRequestMetricsEmitted(t, bufSamples, "GET", sr("HTTPBIN_URL/${}"), 200, "")
				assertRequestMetricsEmitted(t, bufSamples, "GET", sr("HTTPBIN_IP_URL/"), 200, "")
			})
		})

		t.Run("ObjectForm", func(t *testing.T) {
			_, err := rt.RunString(sr(`
			{
				let reqs = [
					{ method: "GET", url: "HTTPBIN_URL/" },
					{ url: "HTTPBIN_IP_URL/", method: "GET"},
				];
				let res = http.batch(reqs);
				for (var key in res) {
					if (res[key].status != 200) { throw new Error("wrong status: " + key + ": " + res[key].status); }
					if (res[key].url != reqs[key].url) { throw new Error("wrong url: " + key + ": " + res[key].url + " != " + reqs[key].url); }
				}
			}`))
			assert.NoError(t, err)
			bufSamples := metrics.GetBufferedSamples(samples)
			assertRequestMetricsEmitted(t, bufSamples, "GET", sr("HTTPBIN_URL/"), 200, "")
			assertRequestMetricsEmitted(t, bufSamples, "GET", sr("HTTPBIN_IP_URL/"), 200, "")
		})

		t.Run("ObjectKeys", func(t *testing.T) {
			_, err := rt.RunString(sr(`
				var reqs = {
					shorthand: "HTTPBIN_URL/get?r=shorthand",
					arr: ["GET", "HTTPBIN_URL/get?r=arr", null, {tags: {name: 'arr'}}],
					obj1: { method: "GET", url: "HTTPBIN_URL/get?r=obj1" },
					obj2: { url: "HTTPBIN_URL/get?r=obj2", params: {tags: {name: 'obj2'}}, method: "GET"},
				};
				var res = http.batch(reqs);
				for (var key in res) {
					if (res[key].status != 200) { throw new Error("wrong status: " + key + ": " + res[key].status); }
					if (res[key].json().args.r != key) { throw new Error("wrong request id: " + key); }
				}`))
			assert.NoError(t, err)
			bufSamples := metrics.GetBufferedSamples(samples)
			assertRequestMetricsEmitted(t, bufSamples, "GET", sr("HTTPBIN_URL/get?r=shorthand"), 200, "")
			assertRequestMetricsEmitted(t, bufSamples, "GET", "arr", 200, "")
			assertRequestMetricsEmitted(t, bufSamples, "GET", sr("HTTPBIN_URL/get?r=obj1"), 200, "")
			assertRequestMetricsEmitted(t, bufSamples, "GET", "obj2", 200, "")
		})

		t.Run("BodyAndParams", func(t *testing.T) {
			testStr := "testbody"
			rt.Set("someStrFile", testStr)
			rt.Set("someBinFile", []byte(testStr))

			_, err := rt.RunString(sr(`
					var reqs = [
						["POST", "HTTPBIN_URL/post", "testbody"],
						["POST", "HTTPBIN_URL/post", someStrFile],
						["POST", "HTTPBIN_URL/post", someBinFile],
						{
							method: "POST",
							url: "HTTPBIN_URL/post",
							test: "test1",
							body: "testbody",
						}, {
							body: someBinFile,
							url: "HTTPBIN_IP_URL/post",
							params: { tags: { name: "myname" } },
							method: "POST",
						}, {
							method: "POST",
							url: "HTTPBIN_IP_URL/post",
							body: {
								hello: "world!",
							},
							params: {
								tags: { name: "myname" },
								headers: { "Content-Type": "application/x-www-form-urlencoded" },
							},
						},
					];
					var res = http.batch(reqs);
					for (var key in res) {
						if (res[key].status != 200) { throw new Error("wrong status: " + key + ": " + res[key].status); }
						if (res[key].json().data != "testbody" && res[key].json().form.hello != "world!") { throw new Error("wrong response for " + key + ": " + res[key].body); }
					}`))
			assert.NoError(t, err)
			bufSamples := metrics.GetBufferedSamples(samples)
			assertRequestMetricsEmitted(t, bufSamples, "POST", sr("HTTPBIN_URL/post"), 200, "")
			assertRequestMetricsEmitted(t, bufSamples, "POST", "myname", 200, "")
		})
	})
	t.Run("POST", func(t *testing.T) {
		_, err := rt.RunString(sr(`
			var res = http.batch([ ["POST", "HTTPBIN_URL/post", { key: "value" }] ]);
			for (var key in res) {
				if (res[key].status != 200) { throw new Error("wrong status: " + key + ": " + res[key].status); }
				if (res[key].json().form.key != "value") { throw new Error("wrong form: " + key + ": " + JSON.stringify(res[key].json().form)); }
			}`))
		assert.NoError(t, err)
		assertRequestMetricsEmitted(t, metrics.GetBufferedSamples(samples), "POST", sr("HTTPBIN_URL/post"), 200, "")
	})
	t.Run("PUT", func(t *testing.T) {
		_, err := rt.RunString(sr(`
			var res = http.batch([ ["PUT", "HTTPBIN_URL/put", { key: "value" }] ]);
			for (var key in res) {
				if (res[key].status != 200) { throw new Error("wrong status: " + key + ": " + res[key].status); }
				if (res[key].json().form.key != "value") { throw new Error("wrong form: " + key + ": " + JSON.stringify(res[key].json().form)); }
			}`))
		assert.NoError(t, err)
		assertRequestMetricsEmitted(t, metrics.GetBufferedSamples(samples), "PUT", sr("HTTPBIN_URL/put"), 200, "")
	})
}
