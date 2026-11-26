import com.cloudbees.hudson.plugins.folder.Folder
import groovy.json.JsonOutput
import hudson.model.Item
import hudson.model.View
import com.cloudbees.hudson.plugins.folder.properties.AuthorizationMatrixProperty
import jenkins.model.Jenkins
import jenkins.plugins.git.GitSCMSource
import org.jenkinsci.plugins.workflow.cps.CpsFlowDefinition
import org.jenkinsci.plugins.workflow.libs.GlobalLibraries
import org.jenkinsci.plugins.workflow.libs.LibraryConfiguration
import org.jenkinsci.plugins.workflow.libs.SCMSourceRetriever

def jenkins = Jenkins.get()
def folder = jenkins.getItem('iquops')
if (folder == null) folder = jenkins.createProject(Folder.class, 'iquops')
if (!(folder instanceof Folder)) throw new IllegalStateException('JENKINS_FOLDER_CONFLICT')
// 仅授予受管目录与子作业的浏览权限。
def access = new AuthorizationMatrixProperty([(Item.READ): ['iquops-viewer'] as Set, (View.READ): ['iquops-viewer'] as Set])
folder.getProperties().removeAll(AuthorizationMatrixProperty.class)
folder.addProperty(access)
folder.save()
def viewer = new org.springframework.security.authentication.UsernamePasswordAuthenticationToken(
    'iquops-viewer', '', [hudson.security.SecurityRealm.AUTHENTICATED_AUTHORITY2])
def viewerReadOnly = jenkins.getACL().hasPermission2(viewer, Jenkins.READ) &&
    !jenkins.getACL().hasPermission2(viewer, Jenkins.ADMINISTER) &&
    folder.getACL().hasPermission2(viewer, Item.READ) &&
    !folder.getACL().hasPermission2(viewer, Item.CONFIGURE) &&
    !folder.getACL().hasPermission2(viewer, Item.BUILD)
if (!viewerReadOnly) throw new IllegalStateException('JENKINS_VIEWER_AUTHORIZATION_INVALID')
// 内置库只来自不可由项目写入的镜像目录，禁止项目选择其他版本。
def library = new LibraryConfiguration('iquops', new SCMSourceRetriever(new GitSCMSource('file:///opt/iquops/library')))
library.setDefaultVersion('main')
library.setImplicit(false)
library.setAllowVersionOverride(false)
library.setIncludeInChangesets(false)
GlobalLibraries.get().setLibraries([library])
GlobalLibraries.get().save()
def configured = GlobalLibraries.get().libraries.find { it.name == 'iquops' }
def retriever = configured?.retriever
def repositoryPath = retriever?.scm?.remote
def sandboxDefinition = new CpsFlowDefinition('echo "health"', true)
if (configured == null || configured.defaultVersion != 'main' || configured.allowVersionOverride ||
    repositoryPath != 'file:///opt/iquops/library' ||
    !new File('/opt/iquops/library/vars/iquopsPipeline.groovy').isFile() ||
    !sandboxDefinition.sandbox) {
    throw new IllegalStateException('JENKINS_RUNTIME_CONFIGURATION_INVALID')
}
new File('/var/jenkins_home/iquops-runtime-health.json').text = JsonOutput.toJson([
    initialized: true,
    security: jenkins.securityRealm.class.name == 'hudson.security.HudsonPrivateSecurityRealm',
    authorization: viewerReadOnly && jenkins.authorizationStrategy.class.name == 'hudson.security.ProjectMatrixAuthorizationStrategy',
    executors: jenkins.numExecutors,
    folder: folder.fullName,
    trustedLibrary: configured.name,
    repositoryPath: repositoryPath,
    defaultVersion: configured.defaultVersion,
    allowVersionOverride: configured.allowVersionOverride,
    sandbox: sandboxDefinition.sandbox,
])
folder.save()
jenkins.save()
