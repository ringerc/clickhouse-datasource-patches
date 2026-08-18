import React from 'react';
import { render, fireEvent, screen } from '@testing-library/react';

import { AdditionalSettingsSection } from './AdditionalSettingsSection';
import { createTestProps } from './helpers';
import { CHCustomSetting, Protocol } from 'types/config';

/**
 * These tests document a v1 → v2 round-trip contract for `customSettings`
 * plus v2's own interaction contract for enforced / header-sourced /
 * JWT-sourced enforced settings.
 *
 * v2 renders a compact base row (Setting / Value / Enforced / Source) plus
 * an on-demand Advanced panel for header- and JWT-source fields. Shared
 * label strings live in src/labels.ts and shared selectors under
 * selectors.components.Config.CustomSettingsConfig.*.
 *
 * A datasource provisioned or edited in v1 with enforcement fields set must
 * survive being edited in v2:
 *
 *   1. Editing the Setting or Value cell of an enforced row in v2 must NOT
 *      drop the enforcement metadata for that row.
 *   2. `onCustomSettingsChange`'s save filter must preserve header-sourced and
 *      JWT-sourced rows (whose `value` is intentionally empty), matching the
 *      v1 filter in CHConfigEditor.tsx.
 */
describe('AdditionalSettingsSection — custom settings round-trip', () => {
  const buildProps = (customSettings: CHCustomSetting[]) => {
    const onOptionsChange = jest.fn();
    const props = createTestProps({
      options: {
        jsonData: {
          host: 'localhost',
          port: 9000,
          protocol: Protocol.Native,
          username: '',
          customSettings,
        },
        secureJsonData: {},
        secureJsonFields: {},
      },
      mocks: { onOptionsChange },
    });
    return { props, onOptionsChange };
  };

  it('preserves enforced/source/headerName/onMissing when editing Setting in v2', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'max_rows_to_read',
        value: '1000',
        enforced: true,
        source: 'static',
        onMissing: 'reject',
      },
    ];
    const { props, onOptionsChange } = buildProps(initial);

    render(<AdditionalSettingsSection {...props} />);

    const settingInput = screen.getByDisplayValue('max_rows_to_read');
    fireEvent.change(settingInput, { target: { value: 'max_rows_to_read_v2' } });
    fireEvent.blur(settingInput);

    expect(onOptionsChange).toHaveBeenCalled();
    const savedCustomSettings: CHCustomSetting[] =
      onOptionsChange.mock.calls[onOptionsChange.mock.calls.length - 1][0].jsonData.customSettings;

    expect(savedCustomSettings).toHaveLength(1);
    expect(savedCustomSettings[0]).toEqual({
      setting: 'max_rows_to_read_v2',
      value: '1000',
      enforced: true,
      source: 'static',
      onMissing: 'reject',
    });
  });

  it('preserves a header-sourced enforced row (empty value) across v2 save filter', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'tenant_id',
        value: '',
        enforced: true,
        source: 'header',
        headerName: 'X-Tenant-Id',
        onMissing: 'reject',
      },
    ];
    const { props, onOptionsChange } = buildProps(initial);

    render(<AdditionalSettingsSection {...props} />);

    const settingInput = screen.getByDisplayValue('tenant_id');
    fireEvent.change(settingInput, { target: { value: 'tenant_id_v2' } });
    fireEvent.blur(settingInput);

    const savedCustomSettings: CHCustomSetting[] =
      onOptionsChange.mock.calls[onOptionsChange.mock.calls.length - 1][0].jsonData.customSettings;

    expect(savedCustomSettings).toHaveLength(1);
    expect(savedCustomSettings[0]).toEqual({
      setting: 'tenant_id_v2',
      value: '',
      enforced: true,
      source: 'header',
      headerName: 'X-Tenant-Id',
      onMissing: 'reject',
    });
  });

  it('preserves a JWT-sourced enforced row (empty value) across v2 save filter', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'user_roles',
        value: '',
        enforced: true,
        source: 'jwt',
        onMissing: 'reject',
        jwtHeaderName: 'X-Id-Token',
        jwtClaimPath: ['realm_access', 'roles'],
        jwtClaimJoin: ',',
        jwtVerify: 'jwks',
        jwtJwksUrl: 'https://idp.example.com/jwks.json',
        jwtIssuer: 'https://idp.example.com/',
        jwtAudience: 'grafana',
      },
    ];
    const { props, onOptionsChange } = buildProps(initial);

    render(<AdditionalSettingsSection {...props} />);

    const settingInput = screen.getByDisplayValue('user_roles');
    fireEvent.change(settingInput, { target: { value: 'user_roles_v2' } });
    fireEvent.blur(settingInput);

    const savedCustomSettings: CHCustomSetting[] =
      onOptionsChange.mock.calls[onOptionsChange.mock.calls.length - 1][0].jsonData.customSettings;

    expect(savedCustomSettings).toHaveLength(1);
    expect(savedCustomSettings[0]).toEqual({
      setting: 'user_roles_v2',
      value: '',
      enforced: true,
      source: 'jwt',
      onMissing: 'reject',
      jwtHeaderName: 'X-Id-Token',
      jwtClaimPath: ['realm_access', 'roles'],
      jwtClaimJoin: ',',
      jwtVerify: 'jwks',
      jwtJwksUrl: 'https://idp.example.com/jwks.json',
      jwtIssuer: 'https://idp.example.com/',
      jwtAudience: 'grafana',
    });
  });

  it('disables the Value input for dynamic-source enforced rows to prevent silent overwrites', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'tenant_id',
        value: '',
        enforced: true,
        source: 'header',
        headerName: 'X-Tenant-Id',
        onMissing: 'reject',
      },
    ];
    const { props } = buildProps(initial);

    render(<AdditionalSettingsSection {...props} />);

    // Locate the disabled Value input in the header-sourced row via its placeholder.
    const valueInput = screen.getByPlaceholderText(/\(from header\)/i) as HTMLInputElement;
    expect(valueInput.disabled).toBe(true);
  });

  it('still drops rows that are empty and non-enforced', () => {
    const initial: CHCustomSetting[] = [
      { setting: 'kept', value: 'v' },
      { setting: '', value: '' },
      { setting: 'dropped_no_value', value: '' },
    ];
    const { props, onOptionsChange } = buildProps(initial);

    render(<AdditionalSettingsSection {...props} />);

    // Trigger a save by editing the first row's value and blurring.
    const valueInput = screen.getByDisplayValue('v');
    fireEvent.change(valueInput, { target: { value: 'v2' } });
    fireEvent.blur(valueInput);

    const savedCustomSettings: CHCustomSetting[] =
      onOptionsChange.mock.calls[onOptionsChange.mock.calls.length - 1][0].jsonData.customSettings;

    expect(savedCustomSettings).toEqual([{ setting: 'kept', value: 'v2' }]);
  });

  it('shows the header-warning banner when a row has source=header', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'tenant_id',
        value: '',
        enforced: true,
        source: 'header',
        headerName: 'X-Tenant-Id',
        onMissing: 'reject',
      },
    ];
    const { props } = buildProps(initial);
    render(<AdditionalSettingsSection {...props} />);
    expect(screen.getByText(/require a trusted upstream proxy/i)).toBeTruthy();
  });

  it('shows the JWT info banner when a row has source=jwt', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'user_roles',
        value: '',
        enforced: true,
        source: 'jwt',
        onMissing: 'reject',
        jwtClaimPath: ['roles'],
      },
    ];
    const { props } = buildProps(initial);
    render(<AdditionalSettingsSection {...props} />);
    // The JWT info-banner title comes from shared labels: "JWT-claim source".
    expect(screen.getByText(/JWT-claim source/i)).toBeTruthy();
  });

  it('auto-expands the advanced panel for a row loaded with a non-static source', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'tenant_id',
        value: '',
        enforced: true,
        source: 'header',
        headerName: 'X-Tenant-Id',
        onMissing: 'reject',
      },
    ];
    const { props } = buildProps(initial);
    render(<AdditionalSettingsSection {...props} />);
    // Advanced-panel HeaderName input should be present without any interaction.
    const headerNameInput = screen.getByDisplayValue('X-Tenant-Id') as HTMLInputElement;
    expect(headerNameInput).toBeTruthy();
  });

  it('auto-locks the Enforce read-only master toggle when any row is enforced', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'max_rows_to_read',
        value: '1000',
        enforced: true,
        source: 'static',
      },
    ];
    const { props } = buildProps(initial);
    const { container } = render(<AdditionalSettingsSection {...props} />);
    const label = screen.getByText('Enforce read-only on all queries');
    // Locate the Switch by walking up from the label to the enclosing Field wrapper,
    // then finding the switch input within it.
    let node: HTMLElement | null = label;
    let toggle: HTMLInputElement | null = null;
    while (node && node !== container) {
      toggle = node.querySelector('input[type="checkbox"]') as HTMLInputElement | null;
      if (toggle) {
        break;
      }
      node = node.parentElement;
    }
    expect(toggle).toBeTruthy();
    expect(toggle!.checked).toBe(true);
    expect(toggle!.disabled).toBe(true);
  });

  it('leaves the Enforce read-only master toggle editable when no row is enforced', () => {
    const initial: CHCustomSetting[] = [{ setting: 'foo', value: 'bar' }];
    const { props } = buildProps(initial);
    const { container } = render(<AdditionalSettingsSection {...props} />);
    const label = screen.getByText('Enforce read-only on all queries');
    let node: HTMLElement | null = label;
    let toggle: HTMLInputElement | null = null;
    while (node && node !== container) {
      toggle = node.querySelector('input[type="checkbox"]') as HTMLInputElement | null;
      if (toggle) {
        break;
      }
      node = node.parentElement;
    }
    expect(toggle).toBeTruthy();
    expect(toggle!.disabled).toBe(false);
  });
});
