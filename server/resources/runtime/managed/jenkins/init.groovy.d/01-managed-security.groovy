import groovy.json.JsonSlurper
import hudson.security.HudsonPrivateSecurityRealm
import hudson.security.ProjectMatrixAuthorizationStrategy
import hudson.security.csrf.DefaultCrumbIssuer
import jenkins.model.Jenkins

new File('/var/jenkins_home/iquops-runtime-health.json').delete()
def config = new JsonSlurper().parse(new File('/run/iquops/control.json'))
def jenkins = Jenkins.get()
def realm = new HudsonPrivateSecurityRealm(false)
realm.createAccount(config.adminUser as String, config.adminPassword as String)
jenkins.setSecurityRealm(realm)
def authorization = new ProjectMatrixAuthorizationStrategy()
authorization.add(Jenkins.ADMINISTER, config.adminUser as String)
authorization.add(Jenkins.READ, 'iquops-viewer')
jenkins.setAuthorizationStrategy(authorization)
jenkins.setCrumbIssuer(new DefaultCrumbIssuer(true))
jenkins.setNumExecutors(0)
jenkins.setSlaveAgentPort(-1)
jenkins.save()
