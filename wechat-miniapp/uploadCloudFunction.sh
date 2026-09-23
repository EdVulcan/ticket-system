set -e
node "${projectPath}/scripts/build-cloud-shared.js" --check
for functionName in bootstrap catalog order address coupon payment paymentCallback assist merchant scheduledTasks
do
  ${installPath} cloud functions deploy --e ${envId} --n ${functionName} --r --project ${projectPath}
done
