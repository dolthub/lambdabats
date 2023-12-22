package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dolthub/lambdabats/wire"
)

func TestTestRunResult(t *testing.T) {
	tr := TestRun{
		Response: wire.RunTestResult{
			Output: `
<?xml version="1.0" encoding="UTF-8"?>
<testsuites time="12.037">
<testsuite name="sql-server-remotesrv.bats" tests="1" failures="0" errors="0" skipped="0" time="12.037" timestamp="2023-12-21T23:48:08" hostname="feb1f90d66dc">
    <testcase classname="sql-server-remotesrv.bats" name="sql-server-remotesrv: push to non-existent database fails" time="12.037" />

</testsuite>
</testsuites>
`,
		},
	}
	res, err := tr.Result("sql-server-remotesrv: push to non-existent database fails")
	assert.NoError(t, err)
	assert.Equal(t, int(res.Status), TestRunResultStatus_Success)

	tr = TestRun{
		Response: wire.RunTestResult{
			Output: `
<?xml version="1.0" encoding="UTF-8"?>
<testsuites time="0">
<testsuite name="sql-server-remotesrv.bats" tests="1" failures="0" errors="0" skipped="1" time="0" timestamp="2023-12-21T23:55:11" hostname="169.254.0.233">
    <testcase classname="sql-server-remotesrv.bats" name="sql-server-remotesrv: create remote branch from remotesapi port as super user" time="0">
        <skipped>this is a skipped test</skipped>
    </testcase>

</testsuite>
</testsuites>
`,
		},
	}
	res, err = tr.Result("sql-server-remotesrv: create remote branch from remotesapi port as super user")
	assert.NoError(t, err)
	assert.Equal(t, int(res.Status), TestRunResultStatus_Skipped)

	tr = TestRun{
		Response: wire.RunTestResult{
			Output: `
<?xml version="1.0" encoding="UTF-8"?>
<testsuites time="0.586">
<testsuite name="sql-server-remotesrv.bats" tests="1" failures="1" errors="0" skipped="0" time="0.586" timestamp="2023-12-21T23:47:55" hostname="feb1f90d66dc">
    <testcase classname="sql-server-remotesrv.bats" name="sql-server-remotesrv: delete remote dirty branch from remotesapi requires force" time="0.586">
        <failure type="failure">(in test file sql-server-remotesrv.bats, line 596)
  ` + "`" + `[[ &quot;$output&quot; =~ &quot;target has uncommitted changes. --force required to overwrite&quot; ]] || false&#39; failed
Successfully initialized dolt data repository.
Successfully initialized dolt data repository.
&#27;[33mcommit r1siavid4po47lmfiese92r9veog4glv &#27;[0m&#27;[33m(&#27;[0m&#27;[36;1mHEAD -&gt; &#27;[0m&#27;[32;1mmain&#27;[0m&#27;[33m) &#27;[0m
Query OK, 3 rows affected (0.00 sec)
Author: Bats Tests &lt;bats@email.fake&gt;
Date:  Thu Dec 21 23:47:55 +0000 2023

	initial names.

Switched to branch &#39;new_branch&#39;
Query OK, 1 row affected (0.00 sec)
Switched to branch &#39;main&#39;
Starting server with Config HP=&quot;localhost:3748&quot;|T=&quot;28800000&quot;|R=&quot;false&quot;|L=&quot;info&quot;|S=&quot;dolt.3748.sock&quot;
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=&quot;Server ready. Accepting connections.&quot;
time=&quot;2023-12-21T23:47:55Z&quot; level=warning msg=&quot;secure_file_priv is set to \&quot;\&quot;, which is insecure.&quot;
time=&quot;2023-12-21T23:47:55Z&quot; level=warning msg=&quot;Any user with GRANT FILE privileges will be able to read any file which the sql-server process can read.&quot;
time=&quot;2023-12-21T23:47:55Z&quot; level=warning msg=&quot;Please consider restarting the server with secure_file_priv set to a safe (or non-existent) directory.&quot;
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=&quot;Starting http server on :5919&quot;
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=NewConnection DisableClientMultiStatements=false connectionID=1
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=ConnectionClosed connectionID=1
Connected successfully!
cloning http://localhost:5919/remote
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=&quot;starting request&quot; method=GetRepoMetadata request_num=1 service=dolt.services.remotesapi.v1alpha1.ChunkStoreServiceServer
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=finished method=GetRepoMetadata repo_path=remote request_num=1 service=dolt.services.remotesapi.v1alpha1.ChunkStoreServiceServer
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=&quot;starting request&quot; method=Root request_num=2 service=dolt.services.remotesapi.v1alpha1.ChunkStoreServiceServer
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=finished method=Root repo_path=remote request_num=2 service=dolt.services.remotesapi.v1alpha1.ChunkStoreServiceServer
Retrieving remote information.
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=&quot;starting request&quot; method=ListTableFiles request_num=3 service=dolt.services.remotesapi.v1alpha1.ChunkStoreServiceServer
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=finished method=ListTableFiles num_appendix_table_files=0 num_table_files=1 repo_path=remote request_num=3 service=dolt.services.remotesapi.v1alpha1.ChunkStoreServiceServer
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=&quot;starting request&quot; method=&quot;GET_/single_symmetric_key_sealed_request/.dolt/noms/vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv?exp=1703203375808&amp;nbf=1703202465808&amp;nonce=Hhf4QUTMcYJ2qo7f&amp;req=f27A6q-xz-fI1dAtvkMJ-Rl1I8Lv2rfyGk8z9MEDZZq-CMUtyeNYAq8ZiK7tjVh6GlslBuESze9qpcY&quot; request_num=4 service=dolt.services.remotesapi.v1alpha1.HttpFileServer
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=finished method=&quot;GET_/single_symmetric_key_sealed_request/.dolt/noms/vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv?exp=1703203375808&amp;nbf=1703202465808&amp;nonce=Hhf4QUTMcYJ2qo7f&amp;req=f27A6q-xz-fI1dAtvkMJ-Rl1I8Lv2rfyGk8z9MEDZZq-CMUtyeNYAq8ZiK7tjVh6GlslBuESze9qpcY&quot; request_num=4 service=dolt.services.remotesapi.v1alpha1.HttpFileServer unsealed_url=.dolt/noms/vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv whole_file=true
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=finished method=GetRepoMetadata repo_path=remote request_num=5 service=dolt.services.remotesapi.v1alpha1.ChunkStoreServiceServer
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=&quot;starting request&quot; method=Root request_num=6 service=dolt.services.remotesapi.v1alpha1.ChunkStoreServiceServer
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=finished method=Root repo_path=remote request_num=6 service=dolt.services.remotesapi.v1alpha1.ChunkStoreServiceServer
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=&quot;starting request&quot; method=StreamDownloadLocations request_num=7 service=dolt.services.remotesapi.v1alpha1.ChunkStoreServiceServer
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=finished method=StreamDownloadLocations num_messages=1 num_ranges=1 num_requested=1 num_urls=1 request_num=7 service=dolt.services.remotesapi.v1alpha1.ChunkStoreServiceServer
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=&quot;starting request&quot; method=&quot;GET_/single_symmetric_key_sealed_request/.dolt/noms/vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv?exp=1703203375852&amp;nbf=1703202465852&amp;nonce=k8HH_RDwKt1sHz26&amp;req=txtrRS3hb0cYb5wcpdl2YNROykHBf_SQmaS-E5US7IuqZxbUE-qtEhkNq1-1MxmxeUQ8xICZji4U_ZU&quot; request_num=8 service=dolt.services.remotesapi.v1alpha1.HttpFileServer
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=finished method=&quot;GET_/single_symmetric_key_sealed_request/.dolt/noms/vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv?exp=1703203375852&amp;nbf=1703202465852&amp;nonce=k8HH_RDwKt1sHz26&amp;req=txtrRS3hb0cYb5wcpdl2YNROykHBf_SQmaS-E5US7IuqZxbUE-qtEhkNq1-1MxmxeUQ8xICZji4U_ZU&quot; read_length=275 read_offset=10633 request_num=8 service=dolt.services.remotesapi.v1alpha1.HttpFileServer unsealed_url=.dolt/noms/vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=&quot;starting request&quot; method=StreamDownloadLocations request_num=9 service=dolt.services.remotesapi.v1alpha1.ChunkStoreServiceServer
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=finished method=StreamDownloadLocations num_messages=1 num_ranges=1 num_requested=1 num_urls=1 request_num=9 service=dolt.services.remotesapi.v1alpha1.ChunkStoreServiceServer
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=&quot;starting request&quot; method=&quot;GET_/single_symmetric_key_sealed_request/.dolt/noms/vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv?exp=1703203375853&amp;nbf=1703202465853&amp;nonce=wOk8fv2puvGD-RK2&amp;req=Wxw7z_R0WJoe6-kgJ0CKznRon3Vcmzdh6KwIqC0ulpsxk5oTmQph1KyUorjOiWLUhZS0Z7mK1pbYayc&quot; request_num=10 service=dolt.services.remotesapi.v1alpha1.HttpFileServer
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=finished method=&quot;GET_/single_symmetric_key_sealed_request/.dolt/noms/vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv?exp=1703203375853&amp;nbf=1703202465853&amp;nonce=wOk8fv2puvGD-RK2&amp;req=Wxw7z_R0WJoe6-kgJ0CKznRon3Vcmzdh6KwIqC0ulpsxk5oTmQph1KyUorjOiWLUhZS0Z7mK1pbYayc&quot; read_length=219 read_offset=5776 request_num=10 service=dolt.services.remotesapi.v1alpha1.HttpFileServer unsealed_url=.dolt/noms/vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=&quot;Server closing listener. No longer accepting connections.&quot;
time=&quot;2023-12-21T23:47:55Z&quot; level=info msg=&quot;http server exited. exit error: http: Server closed&quot;</failure>
    </testcase>

</testsuite>
</testsuites>
`,
		},
	}
	res, err = tr.Result("sql-server-remotesrv: delete remote dirty branch from remotesapi requires force")
	assert.NoError(t, err)
	assert.Equal(t, int(res.Status), TestRunResultStatus_Failure)
	assert.True(t, strings.Contains(res.Output, "http server exited"))
}
