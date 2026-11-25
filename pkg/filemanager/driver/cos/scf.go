package cos

// TODO: revisit para error
const scfFunc = `# -*- coding: utf8 -*-
# SCF config COS trigger, send a callback to the app
from qcloud_cos_v5 import CosConfig
from qcloud_cos_v5 import CosS3Client
from qcloud_cos_v5 import CosServiceError
from qcloud_cos_v5 import CosClientError
import sys
import logging
import requests

logging.basicConfig(level=logging.INFO, stream=sys.stdout)
logging = logging.getLogger()


def main_handler(event, context):
    logging.info("start main handler")
    for record in event['Records']:
        try:
            if "x-cos-meta-callback" not in record['cos']['cosObject']['meta']:
                logging.info("Cannot find callback URL, skiped.")
                return 'Success'
            callback = record['cos']['cosObject']['meta']['x-cos-meta-callback']
            key = record['cos']['cosObject']['key']
            logging.info("Callback URL is " + callback)

            r = requests.get(callback)
            print(r.text)

            

        except Exception as e:
            print(e)
            print('Error getting object {} callback url. '.format(key))
            raise e
            return "Fail"

    return "Success"
`

//
//// CreateSCF creates the callback cloud function
//func CreateSCF(policy *model.Policy, region string) error {
//	// initialize client
//	credential := common.NewCredential(
//		policy.AccessKey,
//		policy.SecretKey,
//	)
//	cpf := profile.NewClientProfile()
//	client, err := scf.NewClient(credential, region, cpf)
//	if err != nil {
//		return err
//	}
//
//	// create callback code data
//	buff := &bytes.Buffer{}
//	bs64 := base64.NewEncoder(base64.StdEncoding, buff)
//	zipWriter := zip.NewWriter(bs64)
//	header := zip.FileHeader{
//		Name:   "callback.py",
//		Method: zip.Deflate,
//	}
//	writer, err := zipWriter.CreateHeader(&header)
//	if err != nil {
//		return err
//	}
//	_, err = io.Copy(writer, strings.NewReader(scfFunc))
//	zipWriter.Close()
//
//	// create cloud function
//	req := scf.NewCreateFunctionRequest()
//	funcName := "cloudreve_" + hashid.HashID(policy.ID, hashid.PolicyID) + strconv.FormatInt(time.Now().Unix(), 10)
//	zipFileBytes, _ := ioutil.ReadAll(buff)
//	zipFileStr := string(zipFileBytes)
//	codeSource := "ZipFile"
//	handler := "callback.main_handler"
//	desc := "callback function for the app"
//	timeout := int64(60)
//	runtime := "Python3.6"
//	req.FunctionName = &funcName
//	req.Code = &scf.Code{
//		ZipFile: &zipFileStr,
//	}
//	req.Handler = &handler
//	req.Description = &desc
//	req.Timeout = &timeout
//	req.Runtime = &runtime
//	req.CodeSource = &codeSource
//
//	_, err = client.CreateFunction(req)
//	if err != nil {
//		return err
//	}
//
//	time.Sleep(time.Duration(5) * time.Second)
//
//	// create trigger
//	server, _ := url.Parse(policy.Server)
//	triggerType := "cos"
//	triggerDesc := `{"event":"cos:ObjectCreated:Post","filter":{"Prefix":"","Suffix":""}}`
//	enable := "OPEN"
//
//	trigger := scf.NewCreateTriggerRequest()
//	trigger.FunctionName = &funcName
//	trigger.TriggerName = &server.Host
//	trigger.Type = &triggerType
//	trigger.TriggerDesc = &triggerDesc
//	trigger.Enable = &enable
//
//	_, err = client.CreateTrigger(trigger)
//	if err != nil {
//		return err
//	}
//
//	return nil
//}
