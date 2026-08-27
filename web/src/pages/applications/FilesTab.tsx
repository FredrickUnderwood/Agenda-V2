import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert,
  App,
  Button,
  Checkbox,
  Empty,
  Input,
  Modal,
  Segmented,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
  Upload,
} from 'antd'
import { InboxOutlined, SafetyCertificateOutlined, UploadOutlined } from '@ant-design/icons'
import type { UploadFile } from 'antd'
import * as api from '@/api/files'
import type { Environment, FileUploadResult, FileVerifyStatus, MachineFile } from '@/api/types'
import { RefreshButton } from '@/components/RefreshButton'
import { errorMessage } from '@/utils/errorMessage'

const ENVS: Environment[] = ['prod', 'stage', 'test']
const CONTAINER_DIR = '/agenda/files'
const DEFAULT_MODE = '0600'

// How each verification outcome is presented. "Unreachable" is deliberately
// neutral rather than red: not being able to ask the machine says nothing about
// the file, and colouring it as a failure would train people to ignore it.
const VERIFY: Record<FileVerifyStatus, { color: string; label: string; hint: string }> = {
  ok: { color: 'green', label: 'OK', hint: 'Present, and its contents still hash to what was uploaded.' },
  changed: {
    color: 'orange',
    label: 'Changed',
    hint: 'Still there, but its contents differ from what the platform wrote. Someone edited or replaced it outside agenda.',
  },
  missing: {
    color: 'red',
    label: 'Missing',
    hint: 'The machine answered, and the file is not at that path. Containers using it will start without it.',
  },
  unreachable: {
    color: 'default',
    label: 'Unknown',
    hint: 'The machine could not be reached, so the file could not be checked. This is not evidence that it is gone.',
  },
  '': { color: 'blue', label: 'Not checked', hint: 'No verification has run for this record yet.' },
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

function formatTime(value: string | null): string {
  if (!value) return '—'
  return new Date(value).toLocaleString()
}

export function FilesTab({ appId }: { appId: number }) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [env, setEnv] = useState<Environment>('prod')
  const [showHistory, setShowHistory] = useState(false)
  const [uploadOpen, setUploadOpen] = useState(false)

  const queryKey = ['applications', appId, 'files', env]
  const { data, isLoading } = useQuery({
    queryKey,
    queryFn: () => api.listApplicationEnvFiles(appId, env),
  })

  const rows = useMemo(() => {
    const all = data?.data ?? []
    return showHistory ? all : all.filter((f) => f.current)
  }, [data, showHistory])

  const verifyMutation = useMutation({
    mutationFn: (fileId: number) => api.verifyMachineFile(fileId),
    onSuccess: (file) => {
      const v = VERIFY[file.last_verify_status]
      const note = file.last_verify_error ? ` — ${file.last_verify_error}` : ''
      if (file.last_verify_status === 'ok') message.success(`${file.file_name} on ${file.machine_name}: ${v.label}`)
      else message.warning(`${file.file_name} on ${file.machine_name}: ${v.label}${note}`)
      queryClient.invalidateQueries({ queryKey })
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const columns = [
    {
      title: 'File',
      dataIndex: 'file_name',
      render: (name: string, row: MachineFile) => (
        <Space direction="vertical" size={0}>
          <Typography.Text className="agenda-mono" strong={row.current}>
            {name}
          </Typography.Text>
          {!row.current && <Tag>superseded</Tag>}
        </Space>
      ),
    },
    { title: 'Machine', dataIndex: 'machine_name', width: 160 },
    {
      title: 'State',
      dataIndex: 'last_verify_status',
      width: 130,
      render: (status: FileVerifyStatus, row: MachineFile) => {
        const v = VERIFY[status] ?? VERIFY['']
        return (
          <Tooltip title={row.last_verify_error || v.hint}>
            <Tag color={v.color}>{v.label}</Tag>
          </Tooltip>
        )
      },
    },
    {
      title: 'Checked',
      dataIndex: 'last_verified_at',
      width: 170,
      render: (value: string | null) => (
        <Typography.Text type="secondary">{formatTime(value)}</Typography.Text>
      ),
    },
    {
      title: 'SHA-256',
      dataIndex: 'sha256',
      width: 140,
      render: (sum: string) => (
        <Tooltip title={sum}>
          <Typography.Text className="agenda-mono" copyable={{ text: sum }}>
            {sum.slice(0, 12)}
          </Typography.Text>
        </Tooltip>
      ),
    },
    {
      title: 'Size',
      dataIndex: 'size',
      width: 90,
      render: (size: number) => formatSize(size),
    },
    { title: 'Mode', dataIndex: 'mode', width: 80 },
    {
      title: 'Uploaded',
      dataIndex: 'created_at',
      width: 200,
      render: (value: string, row: MachineFile) => (
        <Typography.Text type="secondary">
          {formatTime(value)} by {row.username || 'unknown'}
        </Typography.Text>
      ),
    },
    {
      title: '',
      dataIndex: 'actions',
      width: 100,
      render: (_: unknown, row: MachineFile) => (
        <Tooltip title="Ask the machine whether this file is still there and unchanged">
          <Button
            size="small"
            icon={<SafetyCertificateOutlined />}
            loading={verifyMutation.isPending && verifyMutation.variables === row.id}
            onClick={() => verifyMutation.mutate(row.id)}
          >
            Check
          </Button>
        </Tooltip>
      ),
    },
  ]

  const problems = rows.filter((r) => r.current && r.last_verify_status !== 'ok' && r.last_verify_status !== '')

  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Space style={{ justifyContent: 'space-between', width: '100%' }} align="start">
        <Typography.Text type="secondary">
          Files delivered to every machine running this environment and mounted read-only at{' '}
          <Typography.Text className="agenda-mono">{CONTAINER_DIR}</Typography.Text> in each container.
          Contents are never stored by agenda — only the checksum, so a file can be checked but not
          re-sent. Uploads take effect when the instance next starts.
        </Typography.Text>
        <Space>
          <RefreshButton queryKeys={[queryKey]} />
          <Button type="primary" icon={<UploadOutlined />} onClick={() => setUploadOpen(true)}>
            Upload file
          </Button>
        </Space>
      </Space>

      <Space style={{ justifyContent: 'space-between', width: '100%' }}>
        <Segmented
          value={env}
          onChange={(value) => setEnv(value as Environment)}
          options={ENVS.map((e) => ({ label: e.toUpperCase(), value: e }))}
        />
        <Space size="small">
          <Typography.Text type="secondary">Show superseded uploads</Typography.Text>
          <Switch size="small" checked={showHistory} onChange={setShowHistory} />
        </Space>
      </Space>

      {problems.length > 0 && (
        <Alert
          type="warning"
          showIcon
          message={`${problems.length} file(s) are not in the state agenda recorded`}
          description="A missing file means the machine answered and the file is not there — an instance deployed to that machine will start without it. Re-upload to restore it."
        />
      )}

      <Table<MachineFile>
        rowKey="id"
        size="small"
        loading={isLoading}
        dataSource={rows}
        columns={columns}
        pagination={false}
        locale={{
          emptyText: (
            <Empty
              description={`No files uploaded for ${env}. Upload one to have it delivered to every machine in this environment.`}
            />
          ),
        }}
      />

      <UploadFileModal
        appId={appId}
        env={env}
        open={uploadOpen}
        onClose={() => setUploadOpen(false)}
        onUploaded={() => queryClient.invalidateQueries({ queryKey })}
      />
    </Space>
  )
}

function UploadFileModal({
  appId,
  env,
  open,
  onClose,
  onUploaded,
}: {
  appId: number
  env: Environment
  open: boolean
  onClose: () => void
  onUploaded: () => void
}) {
  const { message } = App.useApp()
  const [fileList, setFileList] = useState<UploadFile[]>([])
  const [fileName, setFileName] = useState('')
  const [mode, setMode] = useState(DEFAULT_MODE)
  const [overwrite, setOverwrite] = useState(false)
  const [results, setResults] = useState<FileUploadResult[] | null>(null)

  const selected = fileList[0]?.originFileObj as File | undefined
  const effectiveName = fileName.trim() || fileList[0]?.name || ''

  function reset() {
    setFileList([])
    setFileName('')
    setMode(DEFAULT_MODE)
    setOverwrite(false)
    setResults(null)
  }

  const uploadMutation = useMutation({
    mutationFn: () =>
      api.uploadApplicationEnvFile(appId, {
        env,
        file: selected as File,
        fileName: effectiveName,
        mode,
        overwrite,
      }),
    onSuccess: (res) => {
      setResults(res.data)
      onUploaded()
      const failed = res.data.filter((r) => !r.success)
      if (failed.length === 0) message.success(`Delivered to ${res.data.length} machine(s).`)
      else message.warning(`${failed.length} of ${res.data.length} machine(s) rejected the upload.`)
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  return (
    <Modal
      title={`Upload a file to ${env.toUpperCase()}`}
      open={open}
      onCancel={() => {
        reset()
        onClose()
      }}
      okText="Upload"
      okButtonProps={{ disabled: !selected || !effectiveName }}
      confirmLoading={uploadMutation.isPending}
      onOk={() => uploadMutation.mutate()}
      destroyOnHidden
    >
      <Space direction="vertical" size="middle" style={{ width: '100%' }}>
        <Upload.Dragger
          maxCount={1}
          fileList={fileList}
          // Returning false keeps antd from uploading on its own — the file is
          // held locally and sent by the mutation, with the form's other fields.
          beforeUpload={() => false}
          onChange={({ fileList: next }) => {
            setFileList(next.slice(-1))
            setResults(null)
          }}
        >
          <p className="ant-upload-drag-icon">
            <InboxOutlined />
          </p>
          <p className="ant-upload-text">Click or drag a file here</p>
        </Upload.Dragger>

        <div>
          <Typography.Text type="secondary">Name inside the container</Typography.Text>
          <Input
            className="agenda-mono"
            value={effectiveName}
            placeholder="apple-key.p8"
            onChange={(e) => setFileName(e.target.value)}
          />
          {effectiveName && (
            <Typography.Text type="secondary">
              Your app reads it at{' '}
              <Typography.Text className="agenda-mono" copyable>
                {`${CONTAINER_DIR}/${effectiveName}`}
              </Typography.Text>
            </Typography.Text>
          )}
        </div>

        <div>
          <Typography.Text type="secondary">Permissions</Typography.Text>
          <Input
            className="agenda-mono"
            value={mode}
            placeholder={DEFAULT_MODE}
            onChange={(e) => setMode(e.target.value)}
          />
          <Typography.Text type="secondary">
            Octal, owner-only by default. Widen it only if the container runs as another user.
          </Typography.Text>
        </div>

        <Checkbox checked={overwrite} onChange={(e) => setOverwrite(e.target.checked)}>
          Replace the file if it already exists
        </Checkbox>

        {results && (
          <Space direction="vertical" size={4} style={{ width: '100%' }}>
            {results.map((r) => (
              <Alert
                key={r.machine_id}
                type={r.success ? 'success' : 'error'}
                showIcon
                message={r.machine_name}
                description={r.success ? r.path : r.error}
              />
            ))}
          </Space>
        )}
      </Space>
    </Modal>
  )
}
