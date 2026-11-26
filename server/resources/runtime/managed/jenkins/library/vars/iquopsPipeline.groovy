import groovy.json.JsonOutput
import groovy.json.JsonSlurperClassic
import hudson.model.ParametersAction
import com.cloudbees.groovy.cps.NonCPS
import org.jenkinsci.plugins.workflow.cps.CpsThread
import org.jenkinsci.plugins.workflow.job.WorkflowRun

// 唯一公开步骤只接受隔离构建参数，不接受部署目标、凭据或控制端地址。
def call(Map input = [:]) {
    def allowed = ['nodeImage', 'installCommand', 'testCommand', 'buildCommand', 'dockerfile', 'context', 'healthPath']
    def config = [:]
    input.each { key, value ->
        if (!(key instanceof String) || !allowed.contains(key) || !(value instanceof String) || value.length() > 4096) {
            error('JENKINS_PIPELINE_CONFIG_INVALID')
        }
        config[key] = value
    }
    timeout(time: 120, unit: 'MINUTES') {
        stage('隔离构建与部署') {
            broker('start', config, 0)
            def after = 0
            while (true) {
                def state = broker('status', null, after)
                state.logs.each { item ->
                    echo(item.message)
                    after = item.index + 1
                }
                if (state.status == 'deployed' || state.status == 'succeeded') {
                    currentBuild.description = state.commitSha
                    break
                }
                if (state.status in ['build_failed', 'failed', 'cancelled', 'expired', 'cleanup_required']) {
                    error(state.summary ?: 'JENKINS_RELEASE_FAILED')
                }
                sleep(time: 2, unit: 'SECONDS')
            }
        }
    }
}

@NonCPS
private String releaseRunId() {
    def execution = CpsThread.current()?.execution
    def build = execution?.owner?.executable
    if (!(build instanceof WorkflowRun)) throw new IllegalArgumentException('JENKINS_RUN_INVALID')
    def value = build.getAction(ParametersAction.class)?.getParameter('IQUOPS_RELEASE_RUN_ID')?.value
    if (!(value instanceof String) || !(value ==~ /[a-zA-Z0-9][a-zA-Z0-9_-]{0,95}/)) throw new IllegalArgumentException('JENKINS_RUN_INVALID')
    if (build.parent.fullName != 'iquops/' + value) throw new IllegalArgumentException('JENKINS_RUN_MISMATCH')
    return value
}

@NonCPS
private Map broker(String action, Map input, int after) {
    if (!(action in ['start', 'status']) || after < 0) throw new IllegalArgumentException('JENKINS_BROKER_ACTION_INVALID')
    def runId = releaseRunId()
    def endpoint = '/pipeline/' + action + '/' + runId + '?after=' + after
    def method = action == 'start' ? 'POST' : 'GET'
    def config = new JsonSlurperClassic().parseText(new File('/run/iquops/control.json').getText('UTF-8'))
    def connection = new URL('http://127.0.0.1:' + config.brokerPort + endpoint).openConnection()
    connection.setConnectTimeout(10000)
    connection.setReadTimeout(30000)
    connection.setRequestMethod(method)
    connection.setRequestProperty('Authorization', 'Bearer ' + config.controllerToken)
    if (input != null) {
        connection.setDoOutput(true)
        connection.setRequestProperty('Content-Type', 'application/json')
        connection.outputStream.withCloseable { it.write(JsonOutput.toJson(input).getBytes('UTF-8')) }
    }
    try {
        if (connection.responseCode < 200 || connection.responseCode >= 300) throw new IllegalStateException('JENKINS_BROKER_REQUEST_FAILED')
        return new JsonSlurperClassic().parseText(connection.inputStream.getText('UTF-8')) as Map
    } finally { connection.disconnect() }
}
