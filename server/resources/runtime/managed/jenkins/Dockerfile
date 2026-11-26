FROM docker:29.6.1-cli@sha256:862099ada15c669000bef53aa4cb9d821262829f45b0dda2159ccb276443043b AS docker-cli
FROM node:22-bookworm-slim@sha256:6c74791e557ce11fc957704f6d4fe134a7bc8d6f5ca4403205b2966bd488f6b3 AS node-runtime

FROM node-runtime AS broker
RUN apt-get -o Acquire::Retries=2 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30 update \
    && apt-get install -y --no-install-recommends git openssh-client iptables ca-certificates skopeo \
    && rm -rf /var/lib/apt/lists/*
COPY --from=docker-cli /usr/local/bin/docker /usr/local/bin/docker
COPY *.mjs /opt/iquops/runtime/
WORKDIR /opt/iquops/runtime
CMD ["node", "/opt/iquops/runtime/run-release.mjs"]

FROM jenkins/jenkins:2.568.1-lts-jdk21 AS controller
USER root
RUN for command in curl git ssh tar unzip; do command -v "${command}" >/dev/null; done
COPY --from=node-runtime /usr/local/bin/node /usr/local/bin/node
COPY --from=node-runtime /usr/local/lib/node_modules/ /usr/local/lib/node_modules/
RUN ln -sf ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm && ln -sf ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx
COPY plugins.lock /usr/share/jenkins/ref/plugins.lock
ADD --chown=jenkins:jenkins jenkins-plugins.tar /usr/share/jenkins/ref/plugins/
RUN cd /usr/share/jenkins/ref/plugins && awk -F: '{print $3 "  " $1 ".jpi"}' ../plugins.lock > /tmp/iquops-plugins.sha256 && sha256sum --strict -c /tmp/iquops-plugins.sha256 && rm /tmp/iquops-plugins.sha256
# Jenkins 的覆盖副本确保更新运行包后实际执行新的授权策略及健康检查。
COPY init.groovy.d/01-managed-security.groovy /usr/share/jenkins/ref/init.groovy.d/01-managed-security.groovy.override
COPY init.groovy.d/02-seed-release-jobs.groovy /usr/share/jenkins/ref/init.groovy.d/02-seed-release-jobs.groovy.override
COPY library/ /opt/iquops/library/
RUN git config --system --add safe.directory /opt/iquops/library && git config --system --add safe.directory /opt/iquops/library/.git && git -C /opt/iquops/library init -b main && git -C /opt/iquops/library add . && git -C /opt/iquops/library -c user.name=IQuOps -c user.email=community@iqu.lol commit -m '受管发布库' && chmod -R a+rX /opt/iquops/library /usr/share/jenkins/ref/init.groovy.d /usr/share/jenkins/ref/plugins.lock
ENV JAVA_OPTS="-Djenkins.install.runSetupWizard=false -Dhudson.plugins.git.GitSCM.ALLOW_LOCAL_CHECKOUT=true"
ENV JENKINS_OPTS="--httpPort=8080"
USER jenkins
