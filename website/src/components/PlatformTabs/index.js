import React, {useEffect, useState} from 'react';
import Tabs from '@theme/Tabs';

const defaultPlatformValues = [
  {label: 'Linux', value: 'linux'},
  {label: 'Windows', value: 'windows'},
  {label: 'Mac', value: 'macos'},
];

function detectPlatformValue() {
  if (typeof navigator === 'undefined') {
    return undefined;
  }
  const platform =
    navigator.userAgentData?.platform ||
    navigator.platform ||
    navigator.userAgent ||
    '';
  const normalized = platform.toLowerCase();
  if (normalized.includes('win')) {
    return 'windows';
  }
  if (
    normalized.includes('mac') ||
    normalized.includes('iphone') ||
    normalized.includes('ipad')
  ) {
    return 'macos';
  }
  if (normalized.includes('linux') || normalized.includes('x11')) {
    return 'linux';
  }
  return undefined;
}

export default function PlatformTabs({
  children,
  defaultValue = 'linux',
  detectDefault = true,
  groupId = 'reploy-platform',
  queryString = false,
  values = defaultPlatformValues,
}) {
  const [effectiveDefaultValue, setEffectiveDefaultValue] =
    useState(defaultValue);

  useEffect(() => {
    if (!detectDefault) {
      return;
    }
    const detected = detectPlatformValue();
    if (detected && values.some((value) => value.value === detected)) {
      setEffectiveDefaultValue(detected);
    }
  }, [detectDefault, values]);

  return (
    <div className="platform-tabs">
      <Tabs
        key={`${groupId}:${effectiveDefaultValue}`}
        defaultValue={effectiveDefaultValue}
        groupId={groupId}
        queryString={queryString}
        values={values}>
        {children}
      </Tabs>
    </div>
  );
}
