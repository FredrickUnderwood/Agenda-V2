import { useEffect } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { App, Button, Collapse, Form, Input, Select, Space, Typography } from 'antd'
import type { CollapseProps } from 'antd'
import * as api from '@/api/applications'
import type { Application, DeployMethod, UpdateApplicationRequest } from '@/api/types'
import { errorMessage } from '@/utils/errorMessage'
import { RefreshButton } from '@/components/RefreshButton'
import { DeployConfigFields, DockerHealthCheckFields } from './DeployConfigFields'
import { buildDeployConfig, parseDeployConfig, type DeployConfigForm } from './deployConfig'

interface OverviewFormValues extends DeployConfigForm {
  name: string
  repo_url: string
  deploy_method: DeployMethod
  description?: string
}

export function OverviewTab({ app }: { app: Application }) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [form] = Form.useForm<OverviewFormValues>()
  const method = Form.useWatch('deploy_method', form) ?? app.deploy_method

  useEffect(() => {
    const cfg = parseDeployConfig(app.deploy_config, app.deploy_method)
    form.setFieldsValue({
      name: app.name,
      repo_url: app.repo_url,
      deploy_method: app.deploy_method,
      description: app.description,
      docker: cfg.docker,
      api: cfg.api,
    })
  }, [app, form])

  const updateMutation = useMutation({
    mutationFn: (req: UpdateApplicationRequest) => api.updateApplication(app.id, req),
    onSuccess: () => {
      message.success('Saved.')
      queryClient.invalidateQueries({ queryKey: ['applications', app.id] })
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const handleFinish = (v: OverviewFormValues) => {
    updateMutation.mutate({
      name: v.name,
      repo_url: v.repo_url,
      deploy_method: v.deploy_method,
      description: v.description,
      deploy_config: buildDeployConfig({ docker: v.docker, api: v.api }, v.deploy_method),
    })
  }

  // Each section is a Collapse panel so the (long) form can be folded down to
  // just the module the user cares about. forceRender keeps collapsed fields
  // mounted — otherwise setFieldsValue and submit would drop their values.
  const items: CollapseProps['items'] = [
    {
      key: 'basics',
      label: sectionLabel('Basics', 'Name, repository, and how this app deploys'),
      forceRender: true,
      children: (
        <>
          <Form.Item name="name" label="Name" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="repo_url" label="Repository URL" rules={[{ required: true }]}>
            <Input className="agenda-mono" />
          </Form.Item>
          <Form.Item name="deploy_method" label="Deploy method">
            <Select
              options={[
                { value: 'docker', label: 'Docker Compose' },
                { value: 'api', label: 'HTTP webhook' },
              ]}
            />
          </Form.Item>
          <Form.Item name="description" label="Description" style={{ marginBottom: 0 }}>
            <Input.TextArea rows={2} />
          </Form.Item>
        </>
      ),
    },
    {
      key: 'deploy',
      label: sectionLabel('Deploy config', method === 'api' ? 'Webhook request sent on deploy' : 'Compose file, services, env vars'),
      forceRender: true,
      children: <DeployConfigFields method={method} />,
    },
  ]

  // Health check only applies to the docker deploy method.
  if (method !== 'api') {
    items.push({
      key: 'health',
      label: sectionLabel('Health check', 'Wait for containers to become healthy after deploy'),
      forceRender: true,
      children: <DockerHealthCheckFields />,
    })
  }

  return (
    <div style={{ maxWidth: 680 }}>
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 12 }}>
        <RefreshButton queryKeys={[['applications', app.id]]} />
      </div>
      <Form form={form} layout="vertical" requiredMark={false} onFinish={handleFinish}>
        <Collapse defaultActiveKey={['basics', 'deploy']} items={items} />

        <Space style={{ marginTop: 16 }}>
          <Button type="primary" htmlType="submit" loading={updateMutation.isPending}>
            Save changes
          </Button>
        </Space>
      </Form>
      <Typography.Paragraph type="secondary" style={{ marginTop: 24, fontSize: 12 }}>
        Application #{app.id} · created {new Date(app.created_at).toLocaleString()}
      </Typography.Paragraph>
    </div>
  )
}

function sectionLabel(title: string, hint: string) {
  return (
    <span style={{ display: 'flex', flexDirection: 'column' }}>
      <Typography.Text strong>{title}</Typography.Text>
      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        {hint}
      </Typography.Text>
    </span>
  )
}
